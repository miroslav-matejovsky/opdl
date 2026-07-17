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

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/platform/internal/eventfabric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// Fabric is an Event Fabric backed by an embedded NATS server and its JetStream
// site journal. One runs per platform node. It stamps and publishes events,
// replays and delivers them to projectors, drives durable per-service handlers,
// and reports its own readiness through the same journal.
type Fabric struct {
	cfg          Config
	scope        eventfabric.SiteScope
	node         events.Node
	machineToken string

	srv    *server.Server
	nc     *nats.Conn
	js     jetstream.JetStream
	stream jetstream.Stream

	// applied is the highest journal sequence the node's projector has applied.
	applied atomic.Uint64

	mu     sync.RWMutex
	closed bool
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

// Open starts a node's embedded NATS server, connects to it, and creates or
// validates the site journal. It validates the configuration before opening any
// listener, and leaves nothing running on failure.
//
// An open fabric is usable, not ready: readiness is a conclusion about the
// node's projections and handlers, which is composition's to reach and to state.
func Open(ctx context.Context, descriptor deployment.Descriptor, cfg Config) (*Fabric, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	node := events.NodeFromDescriptor(descriptor)
	scope := eventfabric.NewSiteScope(descriptor.Project, descriptor.Environment, descriptor.Site)

	// Nothing is started for a caller that has already given up: a server torn
	// down mid-start does not reliably stop.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("nats: open on %s: %w", cfg.ClientAddress, err)
	}

	f := &Fabric{cfg: cfg, scope: scope, node: node, machineToken: eventfabric.SafeToken(node.Machine)}

	fail := func(cause error) (*Fabric, error) {
		return nil, fmt.Errorf("nats: open on %s: %w", strings.Join(cfg.Servers, ","), errors.Join(cause, f.abandon(ctx)))
	}

	// Only a storage node runs a server. Every other machine of the site is a
	// client of the ones that do: a server without the journal would add a peer to
	// the journal's metadata group without adding anywhere to keep it, and that
	// group's quorum is what decides whether the site can write at all.
	if cfg.HostsStorage {
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
	for {
		nc, err := nats.Connect(target, natsOptions(f.cfg)...)
		if err == nil {
			return nc, nil
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

// Info returns the fabric's identity and storage disposition: which adapter and
// server this node runs, which journal it is bound to, and whether it stores
// that journal or routes to the nodes that do.
func (f *Fabric) Info() eventfabric.Info {
	name := f.cfg.ServerName
	if !f.cfg.HostsStorage {
		// A machine that runs no server still names itself, so a reader of the
		// journal can tell which node stated the fact.
		name = f.node.Machine
	}
	return eventfabric.Info{
		Adapter:      Name,
		Server:       name,
		Journal:      f.scope.StreamName(),
		HostsStorage: f.cfg.HostsStorage,
		Replicas:     f.cfg.Replicas,
	}
}

// Publish stamps event with its envelope, validates it, and appends it to the
// site journal, returning a receipt once JetStream has durably accepted it. It
// deduplicates by the event's stable publication identity when it has one, so a
// republished fact within the window collapses onto its first acceptance.
func (f *Fabric) Publish(ctx context.Context, event events.Event) (eventfabric.Receipt, error) {
	if err := f.check(ctx); err != nil {
		return eventfabric.Receipt{}, err
	}
	return f.doPublish(ctx, event)
}

// doPublish is the publish path without the closed check, so shutdown can state
// its own stopping event while the connection is still open.
func (f *Fabric) doPublish(ctx context.Context, event events.Event) (eventfabric.Receipt, error) {
	id := events.NewID()
	record, err := events.StampRecord(f.node, id, time.Now(), event)
	if err != nil {
		return eventfabric.Receipt{}, fmt.Errorf("nats: stamp %s: %w", event.EventType(), err)
	}
	record.CausationID, record.CorrelationID = eventfabric.CausalLinks(ctx)
	route, err := eventfabric.NewRoute(f.scope, event.EventType())
	if err != nil {
		return eventfabric.Receipt{}, err
	}
	data, err := eventfabric.Encode(record)
	if err != nil {
		return eventfabric.Receipt{}, err
	}

	dedupID := id
	if identified, ok := event.(eventfabric.Identified); ok {
		dedupID = identified.DedupID()
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
		accepted, decodeErr := eventfabric.Decode(stored.Data)
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
	consumer, err := f.stream.OrderedConsumer(ctx, jetstream.OrderedConsumerConfig{
		DeliverPolicy:  jetstream.DeliverAllPolicy,
		FilterSubjects: []string{f.scope.SubjectFilter()},
	})
	if err != nil {
		return fmt.Errorf("nats: projector consumer on %s: %w", f.scope.StreamName(), err)
	}
	return f.consume(ctx, consumer, func(_ jetstream.Msg, delivery eventfabric.Delivery) error {
		if err := projector.Apply(ctx, delivery); err != nil {
			return fmt.Errorf("nats: apply %s at %d: %w", delivery.Record.Type, delivery.Sequence, err)
		}
		f.applied.Store(delivery.Sequence)
		return nil
	})
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
	consumer, err := f.stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Name:           name,
		Durable:        name,
		FilterSubjects: subjects,
		DeliverPolicy:  jetstream.DeliverAllPolicy,
		AckPolicy:      jetstream.AckExplicitPolicy,
		AckWait:        f.cfg.AckWait,
		MaxDeliver:     f.cfg.MaxDeliver,
	})
	if err != nil {
		return fmt.Errorf("nats: handler consumer %s: %w", name, err)
	}
	return f.consume(ctx, consumer, func(message jetstream.Msg, delivery eventfabric.Delivery) error {
		return f.handle(ctx, handler, message, delivery)
	})
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
	consumer, err := f.stream.Consumer(ctx, name)
	if errors.Is(err, jetstream.ErrConsumerNotFound) {
		return 0, fmt.Errorf("%w: %s", eventfabric.ErrHandlerNotAttached, handler.Name())
	}
	if err != nil {
		return 0, fmt.Errorf("nats: handler consumer %s: %w", name, err)
	}
	info, err := consumer.Info(ctx)
	if err != nil {
		return 0, fmt.Errorf("nats: handler consumer %s info: %w", name, err)
	}
	return info.NumPending + uint64(info.NumAckPending), nil
}

// handle runs one delivery through handler and acknowledges it, leaves it
// unacknowledged for redelivery, or reports exhaustion. A handler failure on a
// delivery that has not reached its redelivery limit is a negative acknowledge
// and the consumer continues; a failure on the last permitted delivery is
// exhaustion, which stops the consumer so the node can be made unready.
func (f *Fabric) handle(ctx context.Context, handler eventfabric.Handler, message jetstream.Msg, delivery eventfabric.Delivery) error {
	if err := handler.Handle(ctx, delivery); err != nil {
		attempt := deliveryAttempt(message)
		if attempt >= uint64(f.cfg.MaxDeliver) {
			return fmt.Errorf("nats: handler %s on %s at %d after %d attempts: %w: %w",
				handler.Name(), delivery.Record.Type, delivery.Sequence, attempt, eventfabric.ErrHandlerExhausted, err)
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

// consume reads messages in order and hands each to apply, until ctx is canceled
// or apply fails. It stops the iterator when ctx is done, so a canceled context
// unblocks a waiting read and returns without error.
func (f *Fabric) consume(ctx context.Context, consumer jetstream.Consumer, apply func(jetstream.Msg, eventfabric.Delivery) error) error {
	iterator, err := consumer.Messages()
	if err != nil {
		return fmt.Errorf("nats: consume %s: %w", f.scope.StreamName(), err)
	}
	defer iterator.Stop()

	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
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
			if errors.Is(err, jetstream.ErrMsgIteratorClosed) {
				return nil
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
	return f.shutdown(stopCtx)
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
// record and its journal sequence, and nothing about the transport.
func toDelivery(message jetstream.Msg) (eventfabric.Delivery, error) {
	metadata, err := message.Metadata()
	if err != nil {
		return eventfabric.Delivery{}, fmt.Errorf("nats: message metadata: %w", err)
	}
	record, err := eventfabric.Decode(message.Data())
	if err != nil {
		return eventfabric.Delivery{}, err
	}
	return eventfabric.Delivery{Record: record, Sequence: metadata.Sequence.Stream}, nil
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
