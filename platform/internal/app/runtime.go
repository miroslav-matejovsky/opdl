package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage/jsonl"
	"github.com/miroslav-matejovsky/opdl/platform/internal/httpapi"
	"github.com/miroslav-matejovsky/opdl/platform/internal/redundancy"
)

// process is everything one running instance was composed from and everything it
// composes a site out of. It is assembled once in Run and passed down, so no
// function below reaches for configuration, identity, or storage on its own.
//
// The two publishing members are deliberately different things. local states
// what this process is doing; the site's fan-out publisher states what the site
// is being told. Which one a component is given decides who hears it, and that
// decision is made here and nowhere else.
type process struct {
	descriptor config.Descriptor
	cfg        *config.Config
	role       redundancy.InstanceRole
	// factory stamps every envelope this process produces, local or journalled.
	factory events.Factory
	// local is the process-local publisher. Its only backend is record, so a fact
	// stated through it reaches the local append-only file and nothing else. It is
	// what composition, ownership, and the transport adapter state through: all
	// three describe a process that may have no journal to write to, and the
	// transport adapter must never describe itself through itself.
	local events.Publisher
	// record is the process's mandatory JSONL backend. A site borrows it as the
	// first backend of its own fan-out publisher, so a journalled fact lands in
	// the same local file in the same order as a local one. The process owns it
	// and closes it; see storage.Borrowed.
	record *jsonl.Backend
}

// monitorInterval is how often a running process re-reads its projection
// progress. It is short enough that a projection falling behind is noticed while
// a deployment tool is still waiting on it, and long enough not to churn the
// Event Fabric client.
//
// It is not how often anything is written. The monitor states a fact only when
// readiness changes, so the interval sets detection latency rather than the size
// of the local record.
const monitorInterval = time.Second

const standbyRetryInterval = 200 * time.Millisecond

// instanceOf returns the running instance's own record: the endpoint it binds and
// the directory it writes.
//
// It reads the descriptor the process was given rather than asking the
// configuration file, because everything a single instance binds or writes is
// resolved onto that instance's record at build time. A machine's two instances
// share one descriptor and one configuration file, so anything read from either
// without a role is a value they would both take.
func instanceOf(descriptor config.Descriptor, role redundancy.InstanceRole) config.Instance {
	return descriptor.Instances.Get(config.Role(role == redundancy.RoleStandby))
}

// peerOf returns the machine's other instance's record. It is empty on a machine
// that deploys only a Primary Instance.
func peerOf(descriptor config.Descriptor, role redundancy.InstanceRole) config.Instance {
	return descriptor.Instances.Get(config.Role(role != redundancy.RoleStandby))
}

// resolveRole validates the requested process role against the deployment policy.
//
// Every packaged launch has an explicit role. A machine that opted out rejects
// standby.
func resolveRole(instance string, hasStandby bool) (redundancy.InstanceRole, error) {
	if instance == "" {
		return "", fmt.Errorf("-instance primary|standby is required")
	}
	role, err := redundancy.ParseRole(instance)
	if err != nil {
		return "", err
	}
	if !hasStandby && role == redundancy.RoleStandby {
		return "", fmt.Errorf("this machine does not run a warm standby: only -instance primary is valid")
	}
	return role, nil
}

// hasEventStorage reports whether this instance was deployed with a site
// journal to reach.
//
// A machine that authored no platform.event_storage block resolves to a
// descriptor with no nats record, and an instance with no nats record runs no
// Event Fabric. That is a whole-deployment property rather than a runtime state:
// it does not change while the process runs, and both of a machine's instances
// share it.
func hasEventStorage(descriptor config.Descriptor, role redundancy.InstanceRole) bool {
	return instanceOf(descriptor, role).Nats != nil
}

// instanceIdentity describes this instance to its own API in the given state.
//
// Nothing here comes from the journal, so it is answerable from the moment the
// process starts: before the projection has caught up, and while it never does.
// That is what makes a Passive instance worth asking.
func instanceIdentity(descriptor config.Descriptor, role redundancy.InstanceRole, state string) api.Instance {
	return api.Instance{
		Machine:     descriptor.Machine,
		Role:        string(role),
		State:       state,
		Address:     instanceOf(descriptor, role).APIAddress,
		PeerAddress: peerOf(descriptor, role).APIAddress,
	}
}

// runProcess binds this instance's API, contends for Primary Ownership, and runs
// the passive or active composition on the outcome.
//
// The order is deliberate. The listener opens first and stays open for the whole
// process, so an instance is reachable in every state and a bind failure stops it
// at startup rather than at a failover. Ownership decides only what it answers.
func runProcess(ctx context.Context, proc process) (runErr error) {
	descriptor, role := proc.descriptor, proc.role

	var windowsMutex string
	if descriptor.Lock != nil {
		windowsMutex = descriptor.Lock.WindowsMutex
	}
	lock, err := redundancy.OpenLock(windowsMutex, role)
	if err != nil {
		return errors.Join(err, proc.local.Publish(ctx, LockOpenFailed{Object: windowsMutex, Error: err.Error()}))
	}
	// The Lock owns kernel handles and a pinned OS thread when not nil. Closing it releases
	// ownership if this process still holds it, so a process that leaves without a
	// clean release still hands over rather than looking like it crashed.
	defer func() { runErr = errors.Join(runErr, lock.Close()) }()

	address := instanceOf(descriptor, role).APIAddress
	passive := httpapi.NewPassiveHandler(func() api.Instance {
		return instanceIdentity(descriptor, role, api.InstanceStatePassive)
	})
	server, err := openInstanceServer(ctx, address, proc.cfg.ReadHeaderTimeout(), passive)
	if err != nil {
		return errors.Join(err, proc.local.Publish(ctx, APIListenFailed{Address: address, Error: err.Error()}))
	}
	// Whatever else happens, the listener drains before the process leaves.
	defer func() { runErr = errors.Join(runErr, server.shutdown(proc.cfg.ShutdownTimeout())) }()

	fmt.Printf("platform: %s listening on %s\n", role, address)
	// A Passive instance is reachable too, and answers a different surface. Which
	// one it is serving is the thing an operator is asking about.
	if err := proc.local.Publish(ctx, APIListening{Address: address, InstanceState: api.InstanceStatePassive}); err != nil {
		return err
	}

	return redundancy.Contend(ctx, proc.local, lock, redundancy.Runtime{
		Passive: func(passiveCtx context.Context) error {
			return runPassive(passiveCtx, proc)
		},
		Active: func(activeCtx context.Context, kind redundancy.ActivationKind) error {
			return runActive(activeCtx, proc, server, kind)
		},
	})
}

// runPassive follows the journal while the machine's other instance is Active,
// and returns when this instance wins ownership or the process is stopping.
//
// It keeps its projection current so a takeover is quick, and it states every
// change in its readiness so deployment tooling can see whether this instance is
// ready to take over. It serves nothing: the listener is already up and
// answering the Passive surface, which needs none of this.
//
// A projection that will not open is not fatal here. An instance that cannot
// follow the journal must still be able to take ownership when the other one
// stops, so this retries until its context ends rather than giving up.
func runPassive(ctx context.Context, proc process) error {
	cfg, role := proc.cfg, proc.role
	// With no journal there is nothing to follow and no readiness to report: this
	// instance is already as current as it can be, and taking over costs it no
	// catch-up. It waits for ownership and nothing else.
	if !hasEventStorage(proc.descriptor, role) {
		fmt.Printf("platform: %s waiting for Primary Ownership; this deployment has no event storage\n", role)
		if err := proc.local.Publish(ctx, StandbyWaiting{}); err != nil {
			return err
		}
		<-ctx.Done()
		return nil
	}

	site, err := openPassiveSite(ctx, proc)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		// Ownership was won, or the process is stopping, before a projection ever
		// opened. Either way there is nothing to run and nothing to close, and
		// neither is a failure.
		return nil
	}
	if err != nil {
		return err
	}

	monitorCtx, stopMonitor := context.WithCancel(context.WithoutCancel(ctx))
	defer stopMonitor()
	monitorDone, err := startFailoverMonitor(monitorCtx, proc.local, site.fabric, redundancy.StatePassive, cfg.LagBound(), nil)
	if err != nil {
		return errors.Join(err, site.close(ctx))
	}

	fmt.Printf("platform: %s caught up and waiting for Primary Ownership\n", role)
	if err := proc.local.Publish(ctx, StandbyWaiting{}); err != nil {
		stopMonitor()
		return errors.Join(err, <-monitorDone, site.close(context.WithoutCancel(ctx)))
	}

	// Wait for ownership or for the process to stop. Either arrives as a canceled
	// context; which one it was is the ownership machine's business, not this
	// function's.
	<-ctx.Done()
	stopMonitor()
	return errors.Join(<-monitorDone, site.close(context.WithoutCancel(ctx)))
}

// openPassiveSite opens the passive projection, retrying until it succeeds or
// ctx ends.
//
// A projection that will not open is not a reason to stop waiting. The instance's
// job while Passive is to be ready to take over, and it can still take over with
// a projection it has not managed to open yet — it just takes longer to catch up
// afterwards. Giving up here would turn a slow journal into a machine with no
// standby at all.
//
// It returns ctx.Err() when the context ended first, which the caller reads as
// "won ownership, or stopping" rather than as a failure.
func openPassiveSite(ctx context.Context, proc process) (*site, error) {
	role := proc.role
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		opened, err := open(ctx, proc, false)
		if err == nil {
			return opened, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		fmt.Fprintf(os.Stderr, "platform: %s standby projection unavailable: %v; waiting for Primary Ownership\n", role, err)
		if attempt == 1 || attempt%10 == 0 {
			if stateErr := proc.local.Publish(ctx, StandbyOpenRetry{Attempt: attempt, Error: err.Error()}); stateErr != nil {
				return nil, errors.Join(err, stateErr)
			}
		}
		select {
		case <-time.After(standbyRetryInterval):
		case <-ctx.Done():
		}
	}
}

// runActive brings this instance's Event Fabric up to readiness and serves the
// whole API until signaled, until its projection falls too far behind the
// journal, or until its site stops carrying events.
//
// It does not open a listener. One is already bound and answering the Passive
// surface, so activation swaps the handler rather than moving the endpoint, and
// the address a caller uses never changes.
func runActive(ctx context.Context, proc process, server *instanceServer, kind redundancy.ActivationKind) error {
	cfg, descriptor, role := proc.cfg, proc.descriptor, proc.role
	// That this instance is activating was stated by the ownership machine before
	// it called this, so there is nothing to report here that is not already in
	// the record.
	fmt.Printf("platform: %s started for %s\n", kind, role)

	if !hasEventStorage(descriptor, role) {
		return runActiveWithoutJournal(ctx, proc, server)
	}

	site, err := open(ctx, proc, true)
	if err != nil {
		return err
	}

	// A projection that falls too far behind stops serving rather than answering
	// from a stale view: the monitor cancels serving when it crosses the bound.
	serveCtx, stopServing := context.WithCancel(ctx)
	defer stopServing()
	monitorCtx, stopMonitor := context.WithCancel(context.WithoutCancel(ctx))
	// The callback runs on the monitor's own goroutine and stops serving; it has
	// no caller to return a publication failure to, so it is reported to the
	// process error stream and the instance still stops serving, which is the part
	// that matters.
	var lagEvent sync.Once
	onLagExceeded := func() {
		lagEvent.Do(func() {
			events.BestEffort(proc.local).State(ctx, ProjectionLagExceeded{LagBound: cfg.LagBound().String()})
		})
		stopServing()
	}
	monitorDone, err := startFailoverMonitor(monitorCtx, proc.local, site.fabric, redundancy.StateActive, cfg.LagBound(), onLagExceeded)
	if err != nil {
		stopMonitor()
		return errors.Join(err, site.close(ctx))
	}
	monitorStopped := false
	stopActiveMonitor := func() error {
		if monitorStopped {
			return nil
		}
		monitorStopped = true
		stopMonitor()
		return <-monitorDone
	}

	address := instanceOf(descriptor, role).APIAddress
	server.serveWith(httpapi.NewHandler(site.commands, site.queries, func() api.Instance {
		return instanceIdentity(descriptor, role, api.InstanceStateActive)
	},
		// exposeSpec is false: the authoritative OpenAPI artifact is
		// api-specifications/openapi.yaml in git, not an endpoint on the runtime.
		false))
	fmt.Printf("platform: %s active, serving on %s\n", role, address)
	// An instance that cannot state that it is serving does not stay serving. The
	// failure takes the place of the reason it would otherwise have stopped, and
	// the ordered shutdown below runs on it exactly as it does on a signal, so the
	// site still releases in the right order.
	serveErr := proc.local.Publish(ctx, APIActive{Address: address, InstanceState: api.InstanceStateActive})
	if serveErr == nil {
		serveErr = awaitStop(serveCtx, site, server)
	}

	// Reverse of startup: HTTP intake stops and in-flight requests drain before
	// anything they could be holding is closed. Only then does the site release,
	// which is what states that this instance has begun stopping.
	monitorErr := stopActiveMonitor()
	shutdownErr := server.shutdown(cfg.ShutdownTimeout())
	closeErr := site.close(context.WithoutCancel(ctx))

	err = errors.Join(serveErr, monitorErr, shutdownErr, closeErr)
	stopped := APIStopped{}
	if err != nil {
		stopped.Error = err.Error()
	}
	// This is a shutdown path with an error to return, so the failure to state
	// that serving stopped is joined onto what actually went wrong rather than
	// replacing it or being dropped.
	stateErr := proc.local.Publish(ctx, stopped)
	if err != nil {
		return errors.Join(err, stateErr)
	}
	return stateErr
}

// runActiveWithoutJournal serves the Active surface of a deployment that has no
// event storage, until signaled or until its listener dies.
//
// The instance holds Primary Ownership and reports itself active, because it is:
// it is the machine's serving instance and there is no other. What it serves is
// bounded by what it has. Every domain operation is a fact to be journalled or a
// query answered from a projection of one, and this deployment has no journal, so
// the domain paths are refused with a reason naming the deployment rather than
// the instance.
//
// There is no site to open, no projection to catch up, and no readiness monitor:
// a lag bound against a journal that does not exist has nothing to measure. That
// is why this is a separate path rather than a flag threaded through the active
// composition, which would carry a site-shaped hole from end to end.
func runActiveWithoutJournal(ctx context.Context, proc process, server *instanceServer) error {
	cfg, descriptor, role := proc.cfg, proc.descriptor, proc.role
	address := instanceOf(descriptor, role).APIAddress

	server.serveWith(httpapi.NewJournallessHandler(func() api.Instance {
		return instanceIdentity(descriptor, role, api.InstanceStateActive)
	}))
	fmt.Printf("platform: %s active, serving on %s (no event storage: domain operations are refused)\n", role, address)

	serveErr := proc.local.Publish(ctx, APIActive{Address: address, InstanceState: api.InstanceStateActive})
	if serveErr == nil {
		select {
		case err := <-server.stopped:
			// The listener died without being asked to. Hand it back so shutdown
			// does not wait on a channel nothing will write to again.
			server.stopped <- err
			serveErr = listenError(err)
		case <-ctx.Done():
		}
	}

	shutdownErr := server.shutdown(cfg.ShutdownTimeout())
	err := errors.Join(serveErr, shutdownErr)
	stopped := APIStopped{}
	if err != nil {
		stopped.Error = err.Error()
	}
	stateErr := proc.local.Publish(ctx, stopped)
	if err != nil {
		return errors.Join(err, stateErr)
	}
	return stateErr
}

// awaitStop blocks until the Active instance should stop serving.
//
// A projector or handler that stops on its own ends serving too. The projection
// is what every query is answered from, so a node that stopped folding the
// journal cannot answer for the site any more; serving on would mean quietly
// returning a view the platform already knows is incomplete.
func awaitStop(ctx context.Context, site *site, server *instanceServer) error {
	select {
	case err := <-server.stopped:
		// The listener died without being asked to. Hand it back so shutdown does
		// not wait on a channel nothing will write to again.
		server.stopped <- err
		return listenError(err)
	case <-site.stopped:
		fmt.Fprintln(os.Stderr, "platform: the event fabric stopped carrying events; shutting down")
	case <-ctx.Done():
	}
	return nil
}

type fabricState struct {
	Applied   uint64
	HighWater uint64
	CaughtUp  bool
}

// progressFabric is the part of an Event Fabric the monitor reads: how far this
// instance's projection has applied of the journal, and whether it is caught up.
type progressFabric interface {
	State(context.Context) (fabricState, error)
}

// startFailoverMonitor watches this instance's projection and states every change
// in its readiness to take over, until ctx ends.
//
// It replaced the status file the runtime rewrote once a second. The facts are
// the same ones and the observation interval is the same; what changed is that
// they are stated when they change instead of restated on a timer. A file could
// be overwritten in place, so restating cost nothing; the local record is
// append-only and fsynced per line, so a heartbeat would grow it by 86,400 lines
// a day per instance to say nothing had happened.
//
// Nothing polls this to learn the instance's lifecycle state. Passive, active,
// stopping, and failed are each already stated by whichever component makes the
// transition, so the monitor states only what none of them can: whether the
// instance is, right now, current enough to be handed the machine.
//
// The first observation is stated synchronously and its failure is returned, so
// an instance whose readiness nothing can record does not start. Afterwards a
// publication failure ends the loop and arrives on the channel for the caller to
// join. It does not stop serving: what an active instance serves from is its
// projection, and a local record that cannot be appended to says nothing about
// that. onLagExceeded is what stops serving, and it is called on every
// observation past the bound rather than only the first, so a caller that
// collapses them does so itself.
func startFailoverMonitor(ctx context.Context, publisher events.Publisher, fabric progressFabric, state redundancy.State, lagBound time.Duration, onLagExceeded func()) (<-chan error, error) {
	var lag redundancy.LagState
	ready := false
	// The first observation always counts as a change, so the record opens with
	// where this instance started rather than only with what it later became.
	first := true

	observe := func() (FailoverReadinessChanged, bool) {
		now := time.Now()
		fact := FailoverReadinessChanged{Ready: true, InstanceState: state.String()}
		st, err := fabric.State(ctx)
		behind := lag.Observe(err != nil || !st.CaughtUp, now)
		fact.Lag = behind.String()
		if err != nil {
			fact.Error = err.Error()
			fact.Ready = false
		} else {
			fact.AppliedSequence, fact.HighWater = st.Applied, st.HighWater
		}
		if redundancy.Exceeds(behind, lagBound) {
			fact.Ready = false
			if onLagExceeded != nil {
				onLagExceeded()
			}
		}
		changed := first || fact.Ready != ready
		first, ready = false, fact.Ready
		return fact, changed
	}

	opening, _ := observe()
	if err := publisher.Publish(ctx, opening); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(monitorInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				done <- nil
				return
			case <-ticker.C:
				fact, changed := observe()
				if !changed {
					continue
				}
				if err := publisher.Publish(ctx, fact); err != nil {
					done <- err
					return
				}
			}
		}
	}()
	return done, nil
}
