package nats

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/eventfabric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/operations"
)

// Fabric is an Event Fabric backed by an embedded NATS server and its JetStream
// site journal. One runs per platform node. It appends completed envelopes,
// replays and delivers them to projectors, and drives durable per-service
// handlers.
type Fabric struct {
	cfg   Config
	scope eventfabric.SiteScope
	// machine is this node's deployment machine name. The adapter needs it to
	// name itself and its durable consumers, not to describe events: which
	// process stated a fact travels in the envelope's origin.
	machine      string
	machineToken string
	observer     *operations.Recorder

	srv    *server.Server
	nc     *nats.Conn
	js     jetstream.JetStream
	stream jetstream.Stream

	// applied is the highest journal sequence the node's projector has applied.
	applied atomic.Uint64

	mu     sync.RWMutex
	closed bool
	// activeHandlers distinguishes a durable that exists on the server from a
	// local loop that is actually consuming it. Startup readiness requires both.
	activeHandlers map[string]bool
}

// Fabric satisfies the Event Fabric contract.
var _ eventfabric.Fabric = (*Fabric)(nil)

// serviceName bounds a durable consumer's service token to characters a NATS
// durable name accepts, so a handler cannot name a consumer the transport
// rejects.
var serviceName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// connectRetryInterval is how often a node retries reaching the site's servers
// while it starts.
const connectRetryInterval = 200 * time.Millisecond

var errConsumerReconnect = errors.New("nats: consumer reset for client reconnect")

// Open starts a node's embedded NATS server, connects to it, and creates or
// validates the site journal. It validates the configuration before opening any
// listener, and leaves nothing running on failure.
//
// An open fabric is usable, not ready: readiness is a conclusion about the
// node's projections and handlers, which is composition's to reach and to state.
func Open(ctx context.Context, descriptor config.Descriptor, cfg Config) (*Fabric, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	scope := eventfabric.NewSiteScope(descriptor.Project, descriptor.Environment, descriptor.Site)

	// Nothing is started for a caller that has already given up: a server torn
	// down mid-start does not reliably stop.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("nats: open on %s: %w", cfg.ClientAddress, err)
	}

	f := &Fabric{
		cfg: cfg, scope: scope, machine: descriptor.Machine,
		machineToken:   eventfabric.SafeToken(descriptor.Machine),
		observer:       operations.FromContext(ctx),
		activeHandlers: make(map[string]bool),
	}

	fail := func(cause error) (*Fabric, error) {
		return nil, fmt.Errorf("nats: open on %s: %w", strings.Join(cfg.Servers, ","), errors.Join(cause, f.abandon(ctx)))
	}

	// Only a storage node runs a server. Every other machine of the site is a
	// client of the ones that do: a server without the journal would add a peer to
	// the journal's metadata group without adding anywhere to keep it, and that
	// group's quorum is what decides whether the site can write at all.
	if cfg.HostsStorage {
		f.observer.Record(ctx, ServerStarting{
			ClientAddress:  cfg.ClientAddress,
			ClusterAddress: cfg.ClusterAddress,
			Routes:         cfg.Routes,
			DataDir:        cfg.DataDir,
		})
		opts, err := serverOptions(cfg)
		if err != nil {
			return nil, err
		}
		srv, err := server.NewServer(opts)
		if err != nil {
			return nil, fmt.Errorf("nats: create server %s: %w", cfg.ServerName, err)
		}
		f.srv = srv

		srv.Start()
		if !srv.ReadyForConnections(cfg.StartupTimeout) {
			return fail(fmt.Errorf("server not ready within %s", cfg.StartupTimeout))
		}
		f.observer.Record(ctx, ServerReady{ClientAddress: cfg.ClientAddress})
	}

	nc, err := f.connect(ctx)
	if err != nil {
		return fail(err)
	}
	f.nc = nc

	js, err := jetstream.New(nc)
	if err != nil {
		return fail(fmt.Errorf("open jetstream: %w", err))
	}
	f.js = js

	stream, err := f.ensureJournal(ctx)
	if err != nil {
		return fail(err)
	}
	f.stream = stream
	f.observer.Record(ctx, JournalReady{
		Journal:      f.scope.StreamName(),
		HostsStorage: cfg.HostsStorage,
		Replicas:     cfg.Replicas,
	})
	return f, nil
}

// connect opens the client connection to the site's servers, waiting for one to
// accept within the startup bound.
//
// A machine that does not store the journal depends on one that does, and a site
// boots in some order, so the first attempt may well find nobody listening yet.
// Retrying until the bound expires is the difference between "the storage node
// is still starting" and "this site has no journal", and only the second is
// worth refusing to start over.
func (f *Fabric) connect(ctx context.Context) (*nats.Conn, error) {
	urls := make([]string, 0, len(f.cfg.Servers))
	for _, addr := range f.cfg.Servers {
		urls = append(urls, "nats://"+addr)
	}
	target := strings.Join(urls, ",")

	deadline := time.Now().Add(f.cfg.StartupTimeout)
	started := time.Now()
	attempt := 0
	for {
		attempt++
		nc, err := nats.Connect(target, natsOptions(f.cfg)...)
		if err == nil {
			f.observeConnection(nc)
			f.observer.Record(ctx, ClientConnected{
				Server:     nc.ConnectedUrlRedacted(),
				Attempts:   attempt,
				DurationMS: time.Since(started).Milliseconds(),
			})
			return nc, nil
		}
		if attempt == 1 || attempt%10 == 0 {
			f.observer.Record(ctx, ClientConnectRetry{Servers: f.cfg.Servers, Attempt: attempt, Error: err.Error()})
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("connect to %s: %w", target, ctxErr)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("connect to %s within %s: %w", target, f.cfg.StartupTimeout, err)
		}
		time.Sleep(connectRetryInterval)
	}
}

// observeConnection reports what the client does on its own: dropping,
// recovering, closing, and failing outside any call.
//
// These callbacks run on the client's own goroutines, with no operation to
// belong to, so they state their facts to the local recorder and never through
// the journal they are describing. A node that has lost its connection is
// precisely the one that cannot publish that it has.
func (f *Fabric) observeConnection(nc *nats.Conn) {
	// The callbacks outlive any call's context, so they record under a background
	// one. Nothing caused them, so there is nothing for a causal link to carry.
	ctx := context.Background()
	nc.SetDisconnectErrHandler(func(connection *nats.Conn, err error) {
		disconnected := ClientDisconnected{LastServer: connection.ConnectedUrlRedacted(), Shutdown: f.isClosed()}
		if err != nil {
			disconnected.Error = err.Error()
		}
		f.observer.Record(ctx, disconnected)
	})
	nc.SetReconnectHandler(func(connection *nats.Conn) {
		f.observer.Record(ctx, ClientReconnected{Server: connection.ConnectedUrlRedacted()})
	})
	nc.SetClosedHandler(func(connection *nats.Conn) {
		closed := ClientClosed{}
		if err := connection.LastError(); err != nil {
			closed.Error = err.Error()
		}
		f.observer.Record(ctx, closed)
	})
	nc.SetErrorHandler(func(_ *nats.Conn, subscription *nats.Subscription, err error) {
		asyncError := ClientAsyncError{Error: err.Error()}
		if subscription != nil {
			asyncError.Subject = subscription.Subject
		}
		f.observer.Record(ctx, asyncError)
	})
}

// Info returns the fabric's identity and storage disposition: which adapter and
// server this node runs, which journal it is bound to, and whether it stores
// that journal or routes to the nodes that do.
func (f *Fabric) Info() eventfabric.Info {
	name := f.cfg.ServerName
	if !f.cfg.HostsStorage {
		// A machine that runs no server still names itself, so a reader of the
		// journal can tell which node stated the fact.
		name = f.machine
	}
	return eventfabric.Info{
		Adapter:      Name,
		Server:       name,
		Journal:      f.scope.StreamName(),
		HostsStorage: f.cfg.HostsStorage,
		Replicas:     f.cfg.Replicas,
	}
}

// Append validates envelope and appends it to the site journal, returning a
// receipt once JetStream has durably accepted it. It deduplicates by the event's
// stable domain identity when it declares one, so a republished fact within the
// window collapses onto its first acceptance.
//
// It takes the envelope as given. The adapter mints no identity, reads no clock,
// encodes no payload, and sets no causal link: by the time an event reaches
// here, what happened is already decided, and only where to put it is not.
func (f *Fabric) Append(ctx context.Context, envelope events.Envelope) (eventfabric.Receipt, error) {
	if err := f.check(ctx); err != nil {
		return eventfabric.Receipt{}, err
	}
	route, err := eventfabric.NewRoute(f.scope, envelope.Type)
	if err != nil {
		return eventfabric.Receipt{}, err
	}
	data, err := events.Encode(envelope)
	if err != nil {
		return eventfabric.Receipt{}, err
	}

	// A fact that declares a stable identity deduplicates on it, so the same
	// decision recomputed after a redelivery collapses onto its first
	// acceptance. A fact without one deduplicates only by its occurrence ID,
	// which recognizes a repeated publish of one envelope but not a fact
	// recomputed from scratch.
	id := envelope.ID
	dedupID := id
	if envelope.StableID != "" {
		dedupID = envelope.StableID
	}
	ack, err := f.js.Publish(ctx, route.Subject(), data, jetstream.WithMsgID(dedupID))
	if err != nil {
		return eventfabric.Receipt{}, fmt.Errorf("nats: publish %s: %w", route.Subject(), err)
	}
	if ack.Duplicate {
		stored, getErr := f.stream.GetMsg(ctx, ack.Sequence)
		if getErr != nil {
			return eventfabric.Receipt{}, fmt.Errorf("nats: read duplicate %s at %d: %w", route.Subject(), ack.Sequence, getErr)
		}
		accepted, decodeErr := events.Decode(stored.Data)
		if decodeErr != nil {
			return eventfabric.Receipt{}, decodeErr
		}
		id = accepted.ID
	}
	return eventfabric.Receipt{ID: id, Sequence: ack.Sequence}, nil
}

// RunProjector replays the site journal in order from the first retained event
// and continues with live events, applying each delivery to projector. It runs
// until ctx is canceled or the projector fails; a projector failure stops
// catch-up and is returned, so the node can be made unready rather than serving
// an incomplete view. A decode failure is treated the same way: the event is not
// skipped.
func (f *Fabric) RunProjector(ctx context.Context, projector eventfabric.Projector) error {
	if err := f.check(ctx); err != nil {
		return err
	}
	f.observer.Record(ctx, ProjectorStarted{})
	for {
		next := f.applied.Load() + 1
		consumer, err := f.attachProjector(ctx, next)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("nats: projector consumer on %s: %w", f.scope.StreamName(), err)
		}
		err = f.consume(ctx, consumer, func(_ jetstream.Msg, delivery eventfabric.Delivery) error {
			if err := projector.Apply(ctx, delivery); err != nil {
				return fmt.Errorf("nats: apply %s at %d: %w", delivery.Envelope.Type, delivery.Sequence, err)
			}
			f.applied.Store(delivery.Sequence)
			return nil
		}, nil)
		if errors.Is(err, errConsumerReconnect) {
			if err := f.waitUntilConnected(ctx); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("nats: projector wait for reconnect: %w", err)
			}
			f.observer.Record(ctx, ProjectorReset{NextSequence: f.applied.Load() + 1})
			continue
		}
		f.observer.Record(ctx, ProjectorStopped{Error: errorText(err)})
		return err
	}
}

func (f *Fabric) attachProjector(ctx context.Context, next uint64) (jetstream.Consumer, error) {
	cfg := orderedConsumerConfig(f.scope.SubjectFilter(), next)
	attempt := 0
	for {
		attempt++
		consumer, err := f.stream.OrderedConsumer(ctx, cfg)
		if err == nil {
			return consumer, nil
		}
		if !retryableConsumerError(ctx, err) {
			return nil, err
		}
		if attempt == 1 || attempt%10 == 0 {
			f.observer.Record(ctx, ProjectorAttachRetry{NextSequence: next, Attempt: attempt, Error: err.Error()})
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(connectRetryInterval):
		}
	}
}

func orderedConsumerConfig(subject string, next uint64) jetstream.OrderedConsumerConfig {
	return jetstream.OrderedConsumerConfig{
		DeliverPolicy:  jetstream.DeliverByStartSequencePolicy,
		OptStartSeq:    next,
		FilterSubjects: []string{subject},
	}
}

// RunHandler runs handler as a durable per-service, per-node reaction to its
// routes, with explicit acknowledgement. A handler that fails leaves its input
// unacknowledged for redelivery; a handler whose delivery is exhausted, or whose
// input cannot be decoded, stops with an error so the node can be made unready
// rather than dropping an event. It runs until ctx is canceled.
func (f *Fabric) RunHandler(ctx context.Context, handler eventfabric.Handler) error {
	if err := f.check(ctx); err != nil {
		return err
	}
	name, err := f.consumerName(handler.Name())
	if err != nil {
		return err
	}
	subjects, err := routeSubjects(handler.Routes())
	if err != nil {
		return err
	}
	f.observer.Record(ctx, HandlerStarted{Handler: handler.Name()})
	for {
		consumer, err := f.attachHandler(ctx, name, subjects)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("nats: handler consumer %s: %w", name, err)
		}
		err = f.consume(ctx, consumer, func(message jetstream.Msg, delivery eventfabric.Delivery) error {
			return f.handle(ctx, handler, message, delivery)
		}, func() { f.setHandlerActive(name, true) })
		f.setHandlerActive(name, false)
		if errors.Is(err, errConsumerReconnect) {
			if err := f.waitUntilConnected(ctx); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("nats: handler %s wait for reconnect: %w", name, err)
			}
			f.observer.Record(ctx, HandlerReset{Handler: handler.Name()})
			continue
		}
		f.observer.Record(ctx, HandlerStopped{Handler: handler.Name(), Error: errorText(err)})
		return err
	}
}

func (f *Fabric) attachHandler(ctx context.Context, name string, subjects []string) (jetstream.Consumer, error) {
	cfg := jetstream.ConsumerConfig{
		Name:           name,
		Durable:        name,
		FilterSubjects: subjects,
		DeliverPolicy:  jetstream.DeliverAllPolicy,
		AckPolicy:      jetstream.AckExplicitPolicy,
		AckWait:        f.cfg.AckWait,
		MaxDeliver:     f.cfg.MaxDeliver,
	}
	attempt := 0
	for {
		attempt++
		consumer, err := f.stream.CreateOrUpdateConsumer(ctx, cfg)
		if err == nil {
			return consumer, nil
		}
		if !retryableConsumerError(ctx, err) {
			return nil, err
		}
		if attempt == 1 || attempt%10 == 0 {
			f.observer.Record(ctx, HandlerAttachRetry{Handler: name, Attempt: attempt, Error: err.Error()})
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(connectRetryInterval):
		}
	}
}

func retryableConsumerError(ctx context.Context, err error) bool {
	return retryableJournalError(ctx, err) ||
		errors.Is(err, nats.ErrTimeout) ||
		errors.Is(err, jetstream.ErrConsumerCreationResponseEmpty) ||
		errors.Is(err, jetstream.ErrConsumerResetResponseEmpty) ||
		errors.Is(err, jetstream.ErrConsumerLeadershipChanged) ||
		errors.Is(err, jetstream.ErrServerShutdown)
}

// errorText renders err for an event payload, and an empty string for no error.
// A loop that was asked to stop and one that gave up are the same event with
// and without this field.
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// HandlerPending returns how many journal events handler's durable consumer has
// yet to acknowledge: the events it has not been given plus the ones it holds
// unacknowledged. Startup waits for it to reach zero, so a node does not serve
// while it still owes the site a decision it already has the input for.
//
// A consumer that does not exist yet is reported as ErrHandlerNotAttached rather
// than as zero pending: "the handler has nothing to do" and "the handler has not
// started" are different answers, and only one of them means a node may serve.
func (f *Fabric) HandlerPending(ctx context.Context, handler eventfabric.Handler) (uint64, error) {
	if err := f.check(ctx); err != nil {
		return 0, err
	}
	name, err := f.consumerName(handler.Name())
	if err != nil {
		return 0, err
	}
	if !f.handlerActive(name) {
		return 0, fmt.Errorf("%w: %s", eventfabric.ErrHandlerNotAttached, handler.Name())
	}
	consumer, err := f.stream.Consumer(ctx, name)
	if err != nil {
		return 0, handlerPendingError(handler.Name(), "lookup", err)
	}
	info, err := consumer.Info(ctx)
	if err != nil {
		return 0, handlerPendingError(handler.Name(), "info", err)
	}
	return info.NumPending + uint64(info.NumAckPending), nil
}

// handlerPendingError preserves a consumer lookup failure while translating a
// missing consumer into the Event Fabric's startup-facing "not attached yet"
// state. A replicated consumer can be locally usable by Messages before its
// Info response has converged on the same server, so both operations can report
// consumer-not-found during startup.
func handlerPendingError(handler, operation string, err error) error {
	if errors.Is(err, jetstream.ErrConsumerNotFound) {
		return fmt.Errorf("%w: %s consumer %s: %w",
			eventfabric.ErrHandlerNotAttached, handler, operation, err)
	}
	return fmt.Errorf("nats: handler %s consumer %s: %w", handler, operation, err)
}

// handle runs one delivery through handler and acknowledges it, leaves it
// unacknowledged for redelivery, or reports exhaustion. A handler failure on a
// delivery that has not reached its redelivery limit is a negative acknowledge
// and the consumer continues; a failure on the last permitted delivery is
// exhaustion, which stops the consumer so the node can be made unready.
func (f *Fabric) handle(ctx context.Context, handler eventfabric.Handler, message jetstream.Msg, delivery eventfabric.Delivery) error {
	// Whatever the handler publishes is a consequence of this delivery, so the
	// cause travels on the context it is given. A handler states facts and never
	// carries the links itself.
	if err := handler.Handle(events.WithCause(ctx, delivery.Envelope), delivery); err != nil {
		attempt := deliveryAttempt(message)
		if attempt >= uint64(f.cfg.MaxDeliver) {
			return fmt.Errorf("nats: handler %s on %s at %d after %d attempts: %w: %w",
				handler.Name(), delivery.Envelope.Type, delivery.Sequence, attempt, eventfabric.ErrHandlerExhausted, err)
		}
		if nakErr := message.Nak(); nakErr != nil {
			return fmt.Errorf("nats: handler %s: negative ack: %w", handler.Name(), nakErr)
		}
		return nil
	}
	if ackErr := message.Ack(); ackErr != nil {
		return fmt.Errorf("nats: handler %s: ack: %w", handler.Name(), ackErr)
	}
	return nil
}

// consume reads messages in order and hands each to apply until ctx is canceled,
// apply fails, or a client reconnect requires the consumer to be recreated. The
// connection status listener explicitly stops the iterator so it cannot remain
// silently attached to the server that disappeared.
func (f *Fabric) consume(ctx context.Context, consumer jetstream.Consumer, apply func(jetstream.Msg, eventfabric.Delivery) error, onStarted func()) error {
	iterator, err := consumer.Messages()
	if err != nil {
		return fmt.Errorf("nats: consume %s: %w", f.scope.StreamName(), err)
	}
	defer iterator.Stop()
	if onStarted != nil {
		onStarted()
	}

	statuses := f.nc.StatusChanged(nats.RECONNECTING)
	defer f.nc.RemoveStatusListener(statuses)
	reconnecting := make(chan struct{}, 1)
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			iterator.Stop()
		case <-statuses:
			reconnecting <- struct{}{}
			iterator.Stop()
		case <-stopped:
		}
	}()

	for {
		message, err := iterator.Next()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if recoverableConsumerReadError(err) {
				// Messages has already issued a new pull request when it reports a
				// missed heartbeat. Keep this iterator alive and retain the signal
				// for operations instead of stopping the platform's background loop.
				f.observer.Record(ctx, ConsumerHeartbeatMissed{Stream: f.scope.StreamName(), Error: err.Error()})
				continue
			}
			select {
			case <-reconnecting:
				return errConsumerReconnect
			default:
			}
			if errors.Is(err, jetstream.ErrConsumerLeadershipChanged) ||
				errors.Is(err, jetstream.ErrConsumerDeleted) ||
				errors.Is(err, jetstream.ErrOrderedConsumerReset) ||
				errors.Is(err, jetstream.ErrServerShutdown) {
				return errConsumerReconnect
			}
			return fmt.Errorf("nats: read %s: %w", f.scope.StreamName(), err)
		}
		delivery, err := toDelivery(message)
		if err != nil {
			return err
		}
		if err := apply(message, delivery); err != nil {
			// A loop cancelled while it was working reports its cancellation, not
			// a verdict on the event. Treating that as a projector or handler
			// failure would make every shutdown that caught one mid-delivery look
			// like the node had broken.
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}

// recoverableConsumerReadError reports iterator errors for which the NATS
// client has already initiated recovery and Next may safely be called again.
func recoverableConsumerReadError(err error) bool {
	return errors.Is(err, jetstream.ErrNoHeartbeat)
}

func (f *Fabric) waitUntilConnected(ctx context.Context) error {
	ticker := time.NewTicker(reconnectWait)
	defer ticker.Stop()
	for {
		switch {
		case f.nc.IsConnected():
			return nil
		case f.nc.IsClosed():
			return nats.ErrConnectionClosed
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// HighWater returns the last sequence the journal has accepted.
func (f *Fabric) HighWater(ctx context.Context) (uint64, error) {
	if err := f.check(ctx); err != nil {
		return 0, err
	}
	info, err := f.stream.Info(ctx)
	if err != nil {
		return 0, fmt.Errorf("nats: journal %s info: %w", f.scope.StreamName(), err)
	}
	return info.State.LastSeq, nil
}

// State reports the fabric's connection and catch-up health, so readiness can be
// derived without reaching into the transport.
func (f *Fabric) State(ctx context.Context) (eventfabric.State, error) {
	if err := f.check(ctx); err != nil {
		return eventfabric.State{}, err
	}
	high, err := f.HighWater(ctx)
	if err != nil {
		return eventfabric.State{}, err
	}
	applied := f.applied.Load()
	return eventfabric.State{
		Connected: f.nc.IsConnected(),
		CaughtUp:  applied >= high,
		HighWater: high,
		Applied:   applied,
	}, nil
}

// Close closes the client and shuts the embedded server down within the
// configured shutdown bound. It is idempotent.
//
// It states nothing. A node announces its own shutdown through the journal
// before it gets here, while the journal can still accept the fact; a transport
// closing itself is not evidence a node stopped cleanly.
func (f *Fabric) Close(ctx context.Context) error {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil
	}
	f.closed = true
	f.mu.Unlock()

	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), f.cfg.ShutdownTimeout)
	defer cancel()
	f.observer.Record(stopCtx, Stopping{})
	err := f.shutdown(stopCtx)
	f.observer.Record(stopCtx, Stopped{Error: errorText(err)})
	return err
}

// abandon tears down a server that never became a working fabric. It runs on the
// Open failure path with its own bounded context, because the caller's may
// already be canceled and a failed start still has to leave nothing running.
func (f *Fabric) abandon(ctx context.Context) error {
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), f.cfg.ShutdownTimeout)
	defer cancel()
	return f.shutdown(stopCtx)
}

// shutdown closes the client and the server, bounding the server's stop by ctx.
// It tolerates a partly built fabric, so it is safe on the Open failure path.
func (f *Fabric) shutdown(ctx context.Context) error {
	if f.nc != nil {
		f.nc.Close()
	}
	if f.srv == nil {
		return nil
	}
	f.srv.Shutdown()

	done := make(chan struct{})
	go func() {
		f.srv.WaitForShutdown()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("nats: server %s did not stop: %w", f.cfg.ServerName, ctx.Err())
	}
}

// check reports whether a call may proceed: a live fabric and a live context.
func (f *Fabric) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.closed {
		return eventfabric.ErrClosed
	}
	return nil
}

func (f *Fabric) isClosed() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.closed
}

func (f *Fabric) setHandlerActive(name string, active bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.activeHandlers[name] = active
}

func (f *Fabric) handlerActive(name string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.activeHandlers[name]
}

// consumerName builds a durable consumer name for one service on this node:
// <site-scope>_<machine-token>_<service>_v1. The service is validated so a
// handler cannot name a consumer the transport rejects.
func (f *Fabric) consumerName(service string) (string, error) {
	if !serviceName.MatchString(service) {
		return "", fmt.Errorf("nats: handler service name %q must match %s", service, serviceName)
	}
	return strings.Join([]string{string(f.scope), f.machineToken, service, "v1"}, "_"), nil
}

// routeSubjects returns the subjects for a handler's routes. A handler with no
// routes is a programming error: it would consume nothing.
func routeSubjects(routes []eventfabric.Route) ([]string, error) {
	if len(routes) == 0 {
		return nil, fmt.Errorf("nats: handler declares no routes")
	}
	subjects := make([]string, 0, len(routes))
	for _, route := range routes {
		subjects = append(subjects, route.Subject())
	}
	return subjects, nil
}

// toDelivery reads a journal message into an Event Fabric delivery: the stored
// envelope and its journal sequence, and nothing about the transport.
func toDelivery(message jetstream.Msg) (eventfabric.Delivery, error) {
	metadata, err := message.Metadata()
	if err != nil {
		return eventfabric.Delivery{}, fmt.Errorf("nats: message metadata: %w", err)
	}
	envelope, err := events.Decode(message.Data())
	if err != nil {
		return eventfabric.Delivery{}, err
	}
	return eventfabric.Delivery{Envelope: envelope, Sequence: metadata.Sequence.Stream}, nil
}

// deliveryAttempt returns how many times a message has been delivered, or zero
// when the metadata cannot be read.
func deliveryAttempt(message jetstream.Msg) uint64 {
	metadata, err := message.Metadata()
	if err != nil {
		return 0
	}
	return metadata.NumDelivered
}
