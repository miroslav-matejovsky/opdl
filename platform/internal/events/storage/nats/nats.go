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
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage/eventfabric"
)

var (
	_ storage.Backend    = (*Backend)(nil)
	_ eventfabric.Fabric = (*Backend)(nil)
)

// Backend is an event storage and Event Fabric backend backed by an embedded NATS
// server and its JetStream site journal. One runs per platform instance. It
// stores completed envelopes, replays and delivers them to projectors, and
// drives durable per-service handlers.
type Backend struct {
	cfg   Config
	scope eventfabric.SiteScope

	machine      string
	machineToken string
	// local states this adapter's own facts: the server it started, the
	// connection it lost, the consumer it reattached. It must not be a publisher
	// that fans out to this backend. A transport describing its own failure
	// through itself either fails again or, worse, appears to succeed, so runtime
	// composition hands this adapter the process-local publisher and keeps the
	// fan-out one for the facts the site is supposed to hear.
	local events.Publisher

	srv    *server.Server
	nc     *nats.Conn
	js     jetstream.JetStream
	stream jetstream.Stream

	applied atomic.Uint64

	mu             sync.RWMutex
	closed         bool
	activeHandlers map[string]bool
}

var serviceName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

const connectRetryInterval = 200 * time.Millisecond

var errConsumerReconnect = errors.New("nats: consumer reset for client reconnect")

// New constructs a Backend from a machine descriptor and configuration. It
// validates the configuration before returning. The returned backend is unstarted;
// call Start to open servers and connections.
//
// local is where this adapter states its own facts. It must be a publisher that
// does not fan out to this backend; see the field it is stored in.
func New(descriptor config.Descriptor, cfg Config, local events.Publisher) (*Backend, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if local == nil {
		return nil, errors.New("nats: local publisher is required")
	}
	scope := eventfabric.NewSiteScope(descriptor.Project, descriptor.Environment, descriptor.Site)
	return &Backend{
		cfg:            cfg,
		scope:          scope,
		machine:        descriptor.Machine,
		machineToken:   eventfabric.SafeToken(descriptor.Machine),
		local:          local,
		activeHandlers: make(map[string]bool),
	}, nil
}

// Start opens the embedded NATS server (if this node hosts storage), connects
// to the site servers, and creates or validates the site journal.
//
// It is a startup path, so a failure to state one of its own facts is returned
// rather than reported: a node that cannot write its local record has not
// started successfully, whatever the transport managed to do.
func (b *Backend) Start(ctx context.Context) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return eventfabric.ErrClosed
	}
	b.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("nats: start on %s: %w", b.cfg.ClientAddress, err)
	}

	fail := func(cause error) error {
		return fmt.Errorf("nats: start on %s: %w", strings.Join(b.cfg.Servers, ","), errors.Join(cause, b.abandon(ctx)))
	}

	if b.cfg.HostsStorage {
		if err := b.startServer(ctx, fail); err != nil {
			return err
		}
	}

	nc, err := b.connect(ctx)
	if err != nil {
		return fail(err)
	}
	b.nc = nc

	js, err := jetstream.New(nc)
	if err != nil {
		return fail(fmt.Errorf("open jetstream: %w", err))
	}
	b.js = js

	stream, err := b.ensureJournal(ctx)
	if err != nil {
		return fail(err)
	}
	b.stream = stream
	if err := b.local.Publish(ctx, JournalReady{
		Journal:      b.scope.StreamName(),
		HostsStorage: b.cfg.HostsStorage,
		Replicas:     b.cfg.Replicas,
	}); err != nil {
		return fail(err)
	}
	return nil
}

// startServer binds this node's embedded server and waits for it to accept
// connections. Only a node that hosts storage runs one.
//
// fail is Start's shutdown-and-describe path: once a server has been started,
// giving up has to take it back down, and every way out from here has to.
func (b *Backend) startServer(ctx context.Context, fail func(error) error) error {
	if err := b.local.Publish(ctx, ServerStarting{
		ClientAddress:     b.cfg.ClientAddress,
		ClusterAddress:    b.cfg.ClusterAddress,
		Routes:            b.cfg.Routes,
		JetStreamStoreDir: b.cfg.JetStreamStoreDir,
	}); err != nil {
		return err
	}
	opts, err := serverOptions(b.cfg)
	if err != nil {
		return err
	}
	srv, err := server.NewServer(opts)
	if err != nil {
		return fmt.Errorf("nats: create server %s: %w", b.cfg.ServerName, err)
	}
	b.srv = srv

	srv.Start()
	if !srv.ReadyForConnections(b.cfg.StartupTimeout) {
		return fail(fmt.Errorf("server not ready within %s", b.cfg.StartupTimeout))
	}
	if err := b.local.Publish(ctx, ServerReady{ClientAddress: b.cfg.ClientAddress}); err != nil {
		return fail(err)
	}
	return nil
}

// Open constructs and starts a Backend. local is where the adapter states its
// own facts; see New.
func Open(ctx context.Context, descriptor config.Descriptor, cfg Config, local events.Publisher) (*Backend, error) {
	b, err := New(descriptor, cfg, local)
	if err != nil {
		return nil, err
	}
	if err := b.Start(ctx); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *Backend) connect(ctx context.Context) (*nats.Conn, error) {
	urls := make([]string, 0, len(b.cfg.Servers))
	for _, addr := range b.cfg.Servers {
		urls = append(urls, "nats://"+addr)
	}
	target := strings.Join(urls, ",")

	deadline := time.Now().Add(b.cfg.StartupTimeout)
	started := time.Now()
	attempt := 0
	for {
		attempt++
		nc, err := nats.Connect(target, natsOptions(b.cfg)...)
		if err == nil {
			b.observeConnection(nc)
			if stateErr := b.local.Publish(ctx, ClientConnected{
				Server:     nc.ConnectedUrlRedacted(),
				Attempts:   attempt,
				DurationMS: time.Since(started).Milliseconds(),
			}); stateErr != nil {
				return nil, stateErr
			}
			return nc, nil
		}
		if attempt == 1 || attempt%10 == 0 {
			if stateErr := b.local.Publish(ctx, ClientConnectRetry{Servers: b.cfg.Servers, Attempt: attempt, Error: err.Error()}); stateErr != nil {
				return nil, errors.Join(err, stateErr)
			}
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("connect to %s: %w", target, ctxErr)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("connect to %s within %s: %w", target, b.cfg.StartupTimeout, err)
		}
		time.Sleep(connectRetryInterval)
	}
}

// observeConnection states what the client connection does on its own. These are
// asynchronous callbacks with no caller to return to, so a publication failure is
// reported to the process error stream and goes no further.
func (b *Backend) observeConnection(nc *nats.Conn) {
	ctx := context.Background()
	nc.SetDisconnectErrHandler(func(connection *nats.Conn, err error) {
		disconnected := ClientDisconnected{LastServer: connection.ConnectedUrlRedacted(), Shutdown: b.isClosed()}
		if err != nil {
			disconnected.Error = err.Error()
		}
		events.BestEffort(b.local).State(ctx, disconnected)
	})
	nc.SetReconnectHandler(func(connection *nats.Conn) {
		events.BestEffort(b.local).State(ctx, ClientReconnected{Server: connection.ConnectedUrlRedacted()})
	})
	nc.SetClosedHandler(func(connection *nats.Conn) {
		closed := ClientClosed{}
		if err := connection.LastError(); err != nil {
			closed.Error = err.Error()
		}
		events.BestEffort(b.local).State(ctx, closed)
	})
	nc.SetErrorHandler(func(_ *nats.Conn, subscription *nats.Subscription, err error) {
		asyncError := ClientAsyncError{Error: err.Error()}
		if subscription != nil {
			asyncError.Subject = subscription.Subject
		}
		events.BestEffort(b.local).State(ctx, asyncError)
	})
}

// Info returns the backend's identity and storage disposition.
func (b *Backend) Info() eventfabric.Info {
	name := b.cfg.ServerName
	if !b.cfg.HostsStorage {
		name = b.machine
	}
	return eventfabric.Info{
		Adapter:      Name,
		Server:       name,
		Journal:      b.scope.StreamName(),
		HostsStorage: b.cfg.HostsStorage,
		Replicas:     b.cfg.Replicas,
	}
}

// Store validates envelope and stores it in the site journal. It deduplicates
// by stable domain identity when declared, or occurrence ID otherwise.
//
// It returns an error if storing fails, and never publishes another event recursively.
func (b *Backend) Store(ctx context.Context, envelope events.Envelope) error {
	if err := b.check(ctx); err != nil {
		return err
	}
	route, err := eventfabric.NewRoute(b.scope, envelope.Type)
	if err != nil {
		return err
	}
	data, err := events.Encode(envelope)
	if err != nil {
		return err
	}

	dedupID := envelope.ID
	if envelope.StableID != "" {
		dedupID = envelope.StableID
	}
	ack, err := b.js.Publish(ctx, route.Subject(), data, jetstream.WithMsgID(dedupID))
	if err != nil {
		return fmt.Errorf("nats: publish %s: %w", route.Subject(), err)
	}
	if ack.Duplicate {
		stored, getErr := b.stream.GetMsg(ctx, ack.Sequence)
		if getErr != nil {
			return fmt.Errorf("nats: read duplicate %s at %d: %w", route.Subject(), ack.Sequence, getErr)
		}
		if _, decodeErr := events.Decode(stored.Data); decodeErr != nil {
			return decodeErr
		}
	}
	return nil
}

// RunProjector replays the site journal in order from the first retained event
// and continues with live events, applying each delivery to projector.
//
// It states its own progress best effort. This loop is what the node's whole
// read model is built from, and a node that stopped folding the journal because
// it could not write a line about itself would have turned a missing record into
// an unserviceable node. The failure is reported to the process error stream, so
// nothing is lost silently.
func (b *Backend) RunProjector(ctx context.Context, projector eventfabric.Projector) error {
	if err := b.check(ctx); err != nil {
		return err
	}
	events.BestEffort(b.local).State(ctx, ProjectorStarted{})
	for {
		next := b.applied.Load() + 1
		consumer, err := b.attachProjector(ctx, next)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("nats: projector consumer on %s: %w", b.scope.StreamName(), err)
		}
		err = b.consume(ctx, consumer, func(_ jetstream.Msg, delivery eventfabric.Delivery) error {
			if err := projector.Apply(ctx, delivery); err != nil {
				return fmt.Errorf("nats: apply %s at %d: %w", delivery.Envelope.Type, delivery.Sequence, err)
			}
			b.applied.Store(delivery.Sequence)
			return nil
		}, nil)
		if errors.Is(err, errConsumerReconnect) {
			if err := b.waitUntilConnected(ctx); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("nats: projector wait for reconnect: %w", err)
			}
			events.BestEffort(b.local).State(ctx, ProjectorReset{NextSequence: b.applied.Load() + 1})
			continue
		}
		events.BestEffort(b.local).State(ctx, ProjectorStopped{Error: errorText(err)})
		return err
	}
}

func (b *Backend) attachProjector(ctx context.Context, next uint64) (jetstream.Consumer, error) {
	cfg := orderedConsumerConfig(b.scope.SubjectFilter(), next)
	attempt := 0
	for {
		attempt++
		consumer, err := b.stream.OrderedConsumer(ctx, cfg)
		if err == nil {
			return consumer, nil
		}
		if !retryableConsumerError(ctx, err) {
			return nil, err
		}
		if attempt == 1 || attempt%10 == 0 {
			events.BestEffort(b.local).State(ctx, ProjectorAttachRetry{NextSequence: next, Attempt: attempt, Error: err.Error()})
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

// RunHandler runs handler as a durable per-service, per-node reaction to its routes.
//
// Like RunProjector it states its own progress best effort, and for the same
// reason: a durable consumer that abandoned its retained work over a local write
// failure would leave the site owed decisions this node had already been given.
func (b *Backend) RunHandler(ctx context.Context, handler eventfabric.Handler) error {
	if err := b.check(ctx); err != nil {
		return err
	}
	name, err := b.consumerName(handler.Name())
	if err != nil {
		return err
	}
	subjects, err := routeSubjects(handler.Routes())
	if err != nil {
		return err
	}
	events.BestEffort(b.local).State(ctx, HandlerStarted{Handler: handler.Name()})
	for {
		consumer, err := b.attachHandler(ctx, name, subjects)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("nats: handler consumer %s: %w", name, err)
		}
		err = b.consume(ctx, consumer, func(message jetstream.Msg, delivery eventfabric.Delivery) error {
			return b.handle(ctx, handler, message, delivery)
		}, func() { b.setHandlerActive(name, true) })
		b.setHandlerActive(name, false)
		if errors.Is(err, errConsumerReconnect) {
			if err := b.waitUntilConnected(ctx); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("nats: handler %s wait for reconnect: %w", name, err)
			}
			events.BestEffort(b.local).State(ctx, HandlerReset{Handler: handler.Name()})
			continue
		}
		events.BestEffort(b.local).State(ctx, HandlerStopped{Handler: handler.Name(), Error: errorText(err)})
		return err
	}
}

func (b *Backend) attachHandler(ctx context.Context, name string, subjects []string) (jetstream.Consumer, error) {
	cfg := jetstream.ConsumerConfig{
		Name:           name,
		Durable:        name,
		FilterSubjects: subjects,
		DeliverPolicy:  jetstream.DeliverAllPolicy,
		AckPolicy:      jetstream.AckExplicitPolicy,
		AckWait:        b.cfg.AckWait,
		MaxDeliver:     b.cfg.MaxDeliver,
	}
	attempt := 0
	for {
		attempt++
		consumer, err := b.stream.CreateOrUpdateConsumer(ctx, cfg)
		if err == nil {
			return consumer, nil
		}
		if !retryableConsumerError(ctx, err) {
			return nil, err
		}
		if attempt == 1 || attempt%10 == 0 {
			events.BestEffort(b.local).State(ctx, HandlerAttachRetry{Handler: name, Attempt: attempt, Error: err.Error()})
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

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// HandlerPending returns how many journal events handler's durable consumer has
// yet to acknowledge.
func (b *Backend) HandlerPending(ctx context.Context, handler eventfabric.Handler) (uint64, error) {
	if err := b.check(ctx); err != nil {
		return 0, err
	}
	name, err := b.consumerName(handler.Name())
	if err != nil {
		return 0, err
	}
	if !b.handlerActive(name) {
		return 0, fmt.Errorf("%w: %s", eventfabric.ErrHandlerNotAttached, handler.Name())
	}
	consumer, err := b.stream.Consumer(ctx, name)
	if err != nil {
		return 0, handlerPendingError(handler.Name(), "lookup", err)
	}
	info, err := consumer.Info(ctx)
	if err != nil {
		return 0, handlerPendingError(handler.Name(), "info", err)
	}
	return info.NumPending + uint64(info.NumAckPending), nil
}

func handlerPendingError(handler, operation string, err error) error {
	if errors.Is(err, jetstream.ErrConsumerNotFound) {
		return fmt.Errorf("%w: %s consumer %s: %w",
			eventfabric.ErrHandlerNotAttached, handler, operation, err)
	}
	return fmt.Errorf("nats: handler %s consumer %s: %w", handler, operation, err)
}

func (b *Backend) handle(ctx context.Context, handler eventfabric.Handler, message jetstream.Msg, delivery eventfabric.Delivery) error {
	if err := handler.Handle(events.WithCause(ctx, delivery.Envelope), delivery); err != nil {
		attempt := deliveryAttempt(message)
		if attempt >= uint64(b.cfg.MaxDeliver) {
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

func (b *Backend) consume(ctx context.Context, consumer jetstream.Consumer, apply func(jetstream.Msg, eventfabric.Delivery) error, onStarted func()) error {
	iterator, err := consumer.Messages()
	if err != nil {
		return fmt.Errorf("nats: consume %s: %w", b.scope.StreamName(), err)
	}
	defer iterator.Stop()
	if onStarted != nil {
		onStarted()
	}

	statuses := b.nc.StatusChanged(nats.RECONNECTING)
	defer b.nc.RemoveStatusListener(statuses)
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
				events.BestEffort(b.local).State(ctx, ConsumerHeartbeatMissed{Stream: b.scope.StreamName(), Error: err.Error()})
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
			return fmt.Errorf("nats: read %s: %w", b.scope.StreamName(), err)
		}
		delivery, err := toDelivery(message)
		if err != nil {
			return err
		}
		if err := apply(message, delivery); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}

func recoverableConsumerReadError(err error) bool {
	return errors.Is(err, jetstream.ErrNoHeartbeat)
}

func (b *Backend) waitUntilConnected(ctx context.Context) error {
	ticker := time.NewTicker(reconnectWait)
	defer ticker.Stop()
	for {
		switch {
		case b.nc != nil && b.nc.IsConnected():
			return nil
		case b.nc != nil && b.nc.IsClosed():
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
func (b *Backend) HighWater(ctx context.Context) (uint64, error) {
	if err := b.check(ctx); err != nil {
		return 0, err
	}
	info, err := b.stream.Info(ctx)
	if err != nil {
		return 0, fmt.Errorf("nats: journal %s info: %w", b.scope.StreamName(), err)
	}
	return info.State.LastSeq, nil
}

// State reports the backend's connection and catch-up health.
func (b *Backend) State(ctx context.Context) (eventfabric.State, error) {
	if err := b.check(ctx); err != nil {
		return eventfabric.State{}, err
	}
	high, err := b.HighWater(ctx)
	if err != nil {
		return eventfabric.State{}, err
	}
	applied := b.applied.Load()
	return eventfabric.State{
		Connected: b.nc != nil && b.nc.IsConnected(),
		CaughtUp:  applied >= high,
		HighWater: high,
		Applied:   applied,
	}, nil
}

// Close closes the client and shuts the embedded server down within the
// configured shutdown bound. It is idempotent.
//
// It states its shutdown through the process-local publisher, never through the
// fan-out one: an adapter that is closing cannot carry a fact about closing, and
// a shutdown that appeared in the journal it just left would be a lie. This is a
// shutdown path with an error to return, so a publication failure is joined with
// whatever the shutdown itself reported rather than replacing it.
func (b *Backend) Close(ctx context.Context) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.mu.Unlock()

	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), b.cfg.ShutdownTimeout)
	defer cancel()
	stoppingErr := b.local.Publish(stopCtx, Stopping{})
	err := b.shutdown(stopCtx)
	stoppedErr := b.local.Publish(stopCtx, Stopped{Error: errorText(err)})
	return errors.Join(err, stoppingErr, stoppedErr)
}

func (b *Backend) abandon(ctx context.Context) error {
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), b.cfg.ShutdownTimeout)
	defer cancel()
	return b.shutdown(stopCtx)
}

func (b *Backend) shutdown(ctx context.Context) error {
	if b.nc != nil {
		b.nc.Close()
	}
	if b.srv == nil {
		return nil
	}
	b.srv.Shutdown()

	done := make(chan struct{})
	go func() {
		b.srv.WaitForShutdown()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("nats: server %s did not stop: %w", b.cfg.ServerName, ctx.Err())
	}
}

func (b *Backend) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed || b.js == nil {
		return eventfabric.ErrClosed
	}
	return nil
}

func (b *Backend) isClosed() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.closed
}

func (b *Backend) setHandlerActive(name string, active bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.activeHandlers[name] = active
}

func (b *Backend) handlerActive(name string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.activeHandlers[name]
}

func (b *Backend) consumerName(service string) (string, error) {
	if !serviceName.MatchString(service) {
		return "", fmt.Errorf("nats: handler service name %q must match %s", service, serviceName)
	}
	return strings.Join([]string{string(b.scope), b.machineToken, service, "v1"}, "_"), nil
}

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

func deliveryAttempt(message jetstream.Msg) uint64 {
	metadata, err := message.Metadata()
	if err != nil {
		return 0
	}
	return metadata.NumDelivered
}
