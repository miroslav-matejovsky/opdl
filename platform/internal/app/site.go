package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/platform/internal/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/eventfabric"
	natsfabric "github.com/miroslav-matejovsky/opdl/platform/internal/eventfabric/nats"
	"github.com/miroslav-matejovsky/opdl/platform/internal/redundancy"
	"github.com/miroslav-matejovsky/opdl/platform/internal/registration"
)

const (
	// backlogPollInterval is how often startup re-reads a handler's pending
	// count. A handler's backlog drains as its own loop acknowledges deliveries,
	// and nothing publishes that progress, so the only way to know it is finished
	// is to ask. The interval is short because it is only paid at startup.
	backlogPollInterval = 50 * time.Millisecond
	// progressTimeout bounds the queries that describe a failed startup. A node
	// that is already giving up must not hang while explaining why.
	progressTimeout = 2 * time.Second
)

// site is this node's running Event Fabric composition: the transport, the
// node-local projection every query is answered from, the durable services that
// react for this machine, and the loops running them.
//
// It is the only place that knows the platform coordinates through events. The
// registration package is handed a Publisher, a Projector, and a Handler; the
// HTTP boundary is handed the two services. Neither can reach the transport.
type site struct {
	fabric     *natsfabric.Fabric
	projection *registration.Projection
	commands   *registration.CommandService
	queries    *registration.QueryService
	role       redundancy.ProcessRole

	// projector is the node-wide ordered consumer: one loop, from the first
	// retained event through live delivery, so nothing falls in a replay-to-live
	// gap.
	projector *runner
	// services are the durable per-node handlers and the loops running them.
	services []*service
	// ready records that this node announced its readiness, which is what makes
	// announcing its shutdown meaningful.
	ready bool

	// stopped closes as soon as any loop returns, so serving can stop with it.
	stopped     chan struct{}
	stoppedOnce sync.Once

	// closeOnce guards release, and closeErr is what it reported. Both the open
	// failure path and serve release a site, and releasing one twice must give
	// the same answer rather than trying to state a shutdown down a closed
	// transport.
	closeOnce sync.Once
	closeErr  error

	catchUpTimeout  time.Duration
	shutdownTimeout time.Duration
}

// service is one durable handler this node runs and the loop running it.
type service struct {
	handler eventfabric.Handler
	runner  *runner
}

// open composes and starts this node's Event Fabric for the process role.
//
// When active is true it returns only once the node is ready to serve: connected
// to its site journal, caught up to a recorded high-water mark, with its handlers
// attached and their retained work done. A node that cannot reach that state does
// not serve, because answering from a projection that has not seen the site's
// history would be answering for a site this process has not caught up with.
//
// When active is false it composes a warm standby: a client-only transport and
// the continuous projector, caught up to the journal, and nothing else. A standby
// opens no durable handler, publishes no readiness, and binds no listener, so it
// follows the site's history without producing a decision or holding an
// active-only capability.
func open(ctx context.Context, descriptor deployment.Descriptor, cfg *config.Config, active bool, role redundancy.ProcessRole) (*site, error) {
	fabricCfg, err := natsConfig(descriptor, cfg)
	if err != nil {
		return nil, err
	}
	// A warm standby never opens the shared journal store or binds the storage
	// node's listeners; the active process owns those. It reaches the journal as a
	// client of the active's server, so it follows history without contending for
	// the storage the fence protects.
	if !active {
		fabricCfg = clientOnly(fabricCfg)
	}
	logEffectiveFabric(descriptor, fabricCfg)
	// Open validates the configuration and probes the journal's storage before it
	// binds a listener, so an unusable data directory or address fails here
	// rather than half way through starting a server.
	fabric, err := natsfabric.Open(ctx, descriptor, fabricCfg)
	if err != nil {
		return nil, err
	}
	info := fabric.Info()
	fmt.Printf("platform: event fabric %s on %s, journal %s (storage=%t replicas=%d)\n",
		info.Server, strings.Join(fabricCfg.Servers, ","), info.Journal,
		info.HostsStorage, info.Replicas)

	s := &site{
		fabric:          fabric,
		projection:      registration.NewProjection(),
		role:            role,
		stopped:         make(chan struct{}),
		catchUpTimeout:  fabricCfg.CatchUpTimeout,
		shutdownTimeout: fabricCfg.ShutdownTimeout,
	}

	if !active {
		if err := s.startStandby(ctx); err != nil {
			return nil, errors.Join(err, s.close(ctx))
		}
		return s, nil
	}

	location, expected := topology(descriptor)
	scope := eventfabric.NewSiteScope(descriptor.Project, descriptor.Environment, descriptor.Site)
	publisher := eventfabric.Publisher(fabric)
	commands, queries, err := registration.Open(publisher, s.projection, location, expected)
	if err != nil {
		return nil, errors.Join(err, s.close(ctx))
	}
	s.commands, s.queries = commands, queries

	handler, err := registration.NewHandler(publisher, s.projection, location, expected, scope)
	if err != nil {
		return nil, errors.Join(err, s.close(ctx))
	}

	if err := s.start(ctx, handler); err != nil {
		return nil, errors.Join(err, s.close(ctx))
	}
	return s, nil
}

// start runs the readiness sequence and returns when this node may serve.
//
// The order is the point. The projector attaches first and stays attached, so
// replay and live delivery are one stream. Catch-up to a captured high-water
// mark happens before any handler runs, because a handler decides from the local
// view and a handler that ran early would decide from a view missing the site's
// history. The handlers then drain what the journal retained for them, and the
// node catches up again to whatever that work published, so it does not serve
// while it still owes the site a decision it has already made. Readiness is
// stated into the journal last and waited for, so the node's own view contains
// the fact that it is ready before anyone can ask it anything.
//
// The whole sequence is bounded: a node that cannot finish it says how far it
// got instead of hanging.
func (s *site) start(ctx context.Context, handler eventfabric.Handler) error {
	catchUpCtx, cancel := context.WithTimeout(ctx, s.catchUpTimeout)
	defer cancel()

	s.projector = s.run(ctx, "projector", func(runCtx context.Context) error {
		return s.fabric.RunProjector(runCtx, s.projection)
	})
	if err := s.catchUp(catchUpCtx, "the retained journal"); err != nil {
		return err
	}

	s.services = append(s.services, &service{
		handler: handler,
		runner: s.run(ctx, "handler "+handler.Name(), func(runCtx context.Context) error {
			return s.fabric.RunHandler(runCtx, handler)
		}),
	})
	if err := s.drain(catchUpCtx); err != nil {
		return err
	}
	if err := s.catchUp(catchUpCtx, "its handlers' retained work"); err != nil {
		return err
	}

	receipt, err := s.fabric.Publish(catchUpCtx, eventfabric.NewReady(s.fabric.Info(), s.projection.Sequence(), s.role.String()))
	if err != nil {
		return fmt.Errorf("state ready: %w", err)
	}
	if err := s.awaitApplied(catchUpCtx, receipt.Sequence, "its own readiness"); err != nil {
		return err
	}
	s.ready = true
	return nil
}

// startStandby runs the projection-only readiness a warm standby needs: it
// attaches the continuous projector and catches up to the journal's high-water
// mark, and nothing else.
//
// It attaches no durable handler, publishes no readiness, and binds no listener,
// so a standby follows the site's history without producing a decision. The same
// projector stays attached for live events, so the standby keeps following after
// it has caught up.
func (s *site) startStandby(ctx context.Context) error {
	catchUpCtx, cancel := context.WithTimeout(ctx, s.catchUpTimeout)
	defer cancel()

	s.projector = s.run(ctx, "projector", func(runCtx context.Context) error {
		return s.fabric.RunProjector(runCtx, s.projection)
	})
	return s.catchUp(catchUpCtx, "the retained journal")
}

// catchUp captures the journal's high-water mark and waits for the projection to
// apply it. The mark is a snapshot: the site keeps publishing, and a node that
// waited for a moving target would never start. Reaching a recorded mark is what
// "caught up" means.
func (s *site) catchUp(ctx context.Context, what string) error {
	high, err := s.fabric.HighWater(ctx)
	if err != nil {
		return fmt.Errorf("read the journal high-water mark: %w", s.readinessError(err))
	}
	return s.awaitApplied(ctx, high, what)
}

// awaitApplied waits for the projection to reach sequence, or for the projector
// to stop trying. Waiting on both matters: a projector that failed will never
// reach the sequence, and a startup that only watched the sequence would burn
// its whole bound before saying so.
func (s *site) awaitApplied(ctx context.Context, sequence uint64, what string) error {
	applied := make(chan error, 1)
	go func() { applied <- s.projection.WaitApplied(ctx, sequence) }()

	select {
	case err := <-applied:
		if err == nil {
			return nil
		}
		return fmt.Errorf("catch up with %s at sequence %d: %w", what, sequence, s.readinessError(err))
	case <-s.projector.done:
		return fmt.Errorf("the projector stopped before %s at sequence %d was applied: %w",
			what, sequence, s.projector.failure())
	}
}

// drain waits for every handler to attach and work through what the journal
// retained for it. A node that served first would be answering for a site whose
// decisions it has not made yet.
func (s *site) drain(ctx context.Context) error {
	for _, svc := range s.services {
		if err := s.drainService(ctx, svc); err != nil {
			return err
		}
	}
	return nil
}

func (s *site) drainService(ctx context.Context, svc *service) error {
	for {
		pending, err := s.fabric.HandlerPending(ctx, svc.handler)
		switch {
		case err == nil && pending == 0:
			return nil
		case err != nil && !errors.Is(err, eventfabric.ErrHandlerNotAttached):
			// A handler that has not attached yet is a "not yet", not a failure:
			// its loop creates the consumer, and this ran first. Anything else is
			// worth giving up over.
			return fmt.Errorf("drain handler %s: %w", svc.handler.Name(), s.readinessError(err))
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("drain handler %s: %w", svc.handler.Name(), s.readinessError(ctx.Err()))
		case <-svc.runner.done:
			return fmt.Errorf("handler %s stopped before its retained work was done: %w",
				svc.handler.Name(), svc.runner.failure())
		case <-time.After(backlogPollInterval):
		}
	}
}

// close releases the node in the reverse of the order start built it, and states
// the node's shutdown into the journal on the way.
//
// Handlers stop first: they are the only role that causes new facts, and a node
// that is leaving should not still be deciding for the site. Each finishes the
// delivery it holds rather than abandoning it half-acknowledged. The node then
// states that it is stopping, while the journal can still accept the fact and
// while its own projector is still attached to hear it. Only then do the
// projector and the transport stop. Every failure is reported; none hides
// another.
//
// It is safe on the open failure path, where there may be nothing to stop, and
// it is idempotent.
func (s *site) close(ctx context.Context) error {
	s.closeOnce.Do(func() { s.closeErr = s.release(ctx) })
	return s.closeErr
}

func (s *site) release(ctx context.Context) error {
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.shutdownTimeout)
	defer cancel()

	var errs []error
	for _, svc := range s.services {
		errs = append(errs, svc.runner.stop())
	}
	// A node that never said it was ready has nothing to say about stopping. It
	// would be stating the end of something the site never heard begin.
	if s.ready {
		if _, err := s.fabric.Publish(stopCtx, eventfabric.NewStopping(s.fabric.Info().Adapter, s.role.String())); err != nil {
			errs = append(errs, fmt.Errorf("state stopping: %w", err))
		}
	}
	errs = append(errs, s.projector.stop(), s.fabric.Close(stopCtx))
	return errors.Join(errs...)
}

// readinessError turns an expired readiness bound into the Event Fabric's
// catch-up timeout, with what the node had actually reached when it gave up. Any
// other failure is its own and is passed through.
func (s *site) readinessError(err error) error {
	if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%w after %s: %s", eventfabric.ErrCatchUpTimeout, s.catchUpTimeout, s.progress())
}

// progress renders how far this node got: what its projection applied of what
// the journal holds, and how much retained work each handler still owed. It is
// the first thing worth knowing about a node that would not start, so it is
// gathered under its own bound rather than the one that just expired.
func (s *site) progress() string {
	ctx, cancel := context.WithTimeout(context.Background(), progressTimeout)
	defer cancel()

	var b strings.Builder
	fmt.Fprintf(&b, "projector applied sequence %d", s.projection.Sequence())
	if high, err := s.fabric.HighWater(ctx); err == nil {
		fmt.Fprintf(&b, " of %d", high)
	} else {
		b.WriteString(" of an unreadable high-water mark")
	}
	for _, svc := range s.services {
		switch pending, err := s.fabric.HandlerPending(ctx, svc.handler); {
		case errors.Is(err, eventfabric.ErrHandlerNotAttached):
			fmt.Fprintf(&b, "; handler %s never attached", svc.handler.Name())
		case err != nil:
			fmt.Fprintf(&b, "; handler %s pending unknown", svc.handler.Name())
		default:
			fmt.Fprintf(&b, "; handler %s has %d pending", svc.handler.Name(), pending)
		}
	}
	return b.String()
}

// runner is one background Event Fabric loop: something to stop, something to
// wait for having stopped, and something whose early return is this node's
// problem rather than something to discover on the next request.
type runner struct {
	name   string
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

// run starts loop in the background and reports its stop to the site, so a
// projector or handler that gives up takes the node's serving with it.
func (s *site) run(ctx context.Context, name string, loop func(context.Context) error) *runner {
	runCtx, cancel := context.WithCancel(ctx)
	r := &runner{name: name, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer func() {
			close(r.done)
			s.stoppedOnce.Do(func() { close(s.stopped) })
		}()
		r.err = loop(runCtx)
	}()
	return r
}

// stop ends the loop and waits for it to finish, reporting why it stopped.
// Waiting is the point: it is what lets the transport close underneath knowing
// nothing is still reading it.
func (r *runner) stop() error {
	if r == nil {
		return nil
	}
	r.cancel()
	<-r.done
	if r.err != nil {
		return fmt.Errorf("%s: %w", r.name, r.err)
	}
	return nil
}

// failure returns why the loop stopped, for a caller that already knows it did.
// A loop that returned nothing still stopped, and a startup waiting on it still
// needs an error to report.
func (r *runner) failure() error {
	if r.err != nil {
		return r.err
	}
	return fmt.Errorf("%s stopped", r.name)
}

// natsConfig composes the Event Fabric adapter's configuration from the descriptor's
// derived topology and runtime data paths/timeouts from the configuration file.
//
// It takes no process role. Every role on the machine composes the same
// configuration from the same descriptor topology; only clientOnly distinguishes
// a process that does not hold the fence, and it removes ownership rather than
// selecting a different endpoint.
func natsConfig(descriptor deployment.Descriptor, cfg *config.Config) (natsfabric.Config, error) {
	fabricCfg, err := natsfabric.DefaultConfig(descriptor)
	if err != nil {
		return natsfabric.Config{}, err
	}
	settings := cfg.EventFabric().Nats

	fabricCfg.DataDir = nodeDataDir(settings.DataDir, descriptor)
	fabricCfg.Username, fabricCfg.Password = cfg.Credentials()
	fabricCfg.ShutdownTimeout = cfg.ShutdownTimeout()

	startupTimeout, err := time.ParseDuration(settings.StartupTimeout)
	if err != nil {
		return natsfabric.Config{}, fmt.Errorf("event fabric: startup timeout %q: %w", settings.StartupTimeout, err)
	}
	fabricCfg.StartupTimeout = startupTimeout
	catchUpTimeout, err := time.ParseDuration(settings.CatchUpTimeout)
	if err != nil {
		return natsfabric.Config{}, fmt.Errorf("event fabric: catch-up timeout %q: %w", settings.CatchUpTimeout, err)
	}
	fabricCfg.CatchUpTimeout = catchUpTimeout

	return fabricCfg, nil
}

// clientOnly turns a storage node's Event Fabric configuration into a client-only
// one for a warm standby. A storage node's active process binds the server and owns
// the JetStream store; its standby must do neither, or two processes would try to
// bind the same ports and open the same journal directory. The standby keeps the
// servers it already reaches the journal through and drops everything it would
// otherwise bind or store.
//
// A node that does not store the journal is already a client and is returned
// unchanged.
func clientOnly(cfg natsfabric.Config) natsfabric.Config {
	if !cfg.HostsStorage {
		return cfg
	}
	cfg.HostsStorage = false
	// cfg.Servers is kept whole and unchanged: the local server first, then every
	// peer storage server. That list is what the standby reaches the journal
	// through, and its first entry is the address the local active process is
	// serving on right now. Narrowing or substituting it here is exactly the bug
	// this shape exists to prevent, because the standby would then wait on an
	// address no process is listening on.
	cfg.ClientAddress = ""
	cfg.ClusterAddress = ""
	cfg.Routes = nil
	cfg.DataDir = ""
	return cfg
}

// logEffectiveFabric prints the Event Fabric endpoints this process actually
// composed, before anything is bound or connected.
//
// It exists so the four cases a machine can be in are distinguishable from the
// process output alone:
//
//	active storage server         endpoint=X binds=true  storage=true
//	client-only local standby     endpoint=X binds=false storage=false, X in servers
//	client-only non-storage node  endpoint=X binds=false storage=false, X not in servers
//	storage node after promotion  endpoint=X binds=true  storage=true
//
// Those look alike in every other log line, and telling them apart after the
// fact is what a failure to reach the journal actually needs. endpoint is the
// machine's own address from the descriptor and is printed whether or not this
// process binds it, so a standby and the active process it follows are visibly
// talking about the same endpoint.
//
// Every value is a single token so the line can be parsed. No credential is
// printed, and there is no monitor endpoint to print.
func logEffectiveFabric(descriptor deployment.Descriptor, cfg natsfabric.Config) {
	cluster := "none"
	if len(cfg.Routes) > 0 {
		cluster = cfg.ClusterAddress
	}
	routes := "none"
	if len(cfg.Routes) > 0 {
		routes = strings.Join(cfg.Routes, ",")
	}
	fmt.Printf("platform: event fabric configuration endpoint=%s binds=%t cluster=%s servers=%s routes=%s storage=%t replicas=%d\n",
		descriptor.EventFabric.Nats.ClientAddress, cfg.ClientAddress != "", cluster,
		strings.Join(cfg.Servers, ","), routes, cfg.HostsStorage, cfg.Replicas)
}

// nodeDataDir places this node's journal storage in its own subdirectory of the
// configured data directory, named after the deployment identity the machine was
// built with.
//
// The platform creates it and never removes it: the journal is the site's
// history, and a runtime that tidied its own storage away could delete the only
// copy of facts nothing else holds. The subdirectory is what lets one host run
// several machines, and what stops two machines from ever adopting each other's
// store.
func nodeDataDir(dataDir string, descriptor deployment.Descriptor) string {
	node := strings.Join([]string{
		descriptor.Project, descriptor.Environment, descriptor.Site, descriptor.Machine,
	}, "-")
	return filepath.Join(dataDir, node)
}

// topology reads the trusted registration topology from the descriptor: this
// machine, and every machine of its site including itself. A registration needs
// every one of them to confirm, so the expected set is the site's static
// membership and never the members that happen to be reachable.
func topology(descriptor deployment.Descriptor) (self registration.Location, expected []registration.Location) {
	self = registration.Location{Machine: descriptor.Machine, IP: descriptor.IP}
	expected = make([]registration.Location, 0, len(descriptor.EventFabric.Peers)+1)
	expected = append(expected, self)
	for _, peer := range descriptor.EventFabric.Peers {
		expected = append(expected, registration.Location{Machine: peer.Machine, IP: peer.IP})
	}
	return self, expected
}
