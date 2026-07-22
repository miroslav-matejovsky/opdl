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
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage/eventfabric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/httpapi"
	"github.com/miroslav-matejovsky/opdl/platform/internal/operations"
	"github.com/miroslav-matejovsky/opdl/platform/internal/redundancy"
)

// statusInterval is how often a running process rewrites its local status file. It
// is short enough to be useful to a watching deployment tool and long enough not
// to churn the disk.
const statusInterval = time.Second

const standbyRetryInterval = 200 * time.Millisecond

const unknownLag = "unknown"

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
func runProcess(ctx context.Context, cfg *config.Config, descriptor config.Descriptor, role redundancy.InstanceRole, factory events.Factory) (runErr error) {
	observer := operations.FromContext(ctx)
	statusPath := redundancy.StatusPath(instanceOf(descriptor, role).RuntimeDir)
	if err := redundancy.PrepareStatusDir(statusPath); err != nil {
		observer.Record(ctx, StatusDirFailed{Path: statusPath, Error: err.Error()})
		return err
	}

	var windowsMutex string
	if descriptor.Lock != nil {
		windowsMutex = descriptor.Lock.WindowsMutex
	}
	lock, err := redundancy.OpenLock(windowsMutex, role)
	if err != nil {
		observer.Record(ctx, LockOpenFailed{Object: windowsMutex, Error: err.Error()})
		return err
	}
	// The Lock owns kernel handles and a pinned OS thread when not nil. Closing it releases
	// ownership if this process still holds it, so a process that leaves without a
	// clean release still hands over rather than looking like it crashed.
	defer func() { runErr = errors.Join(runErr, lock.Close()) }()

	address := instanceOf(descriptor, role).APIAddress
	passive := httpapi.NewPassiveHandler(func() api.Instance {
		return instanceIdentity(descriptor, role, api.InstanceStatePassive)
	})
	server, err := openInstanceServer(ctx, address, cfg.ReadHeaderTimeout(), passive)
	if err != nil {
		observer.Record(ctx, APIListenFailed{Address: address, Error: err.Error()})
		return err
	}
	// Whatever else happens, the listener drains before the process leaves.
	defer func() { runErr = errors.Join(runErr, server.shutdown(cfg.ShutdownTimeout())) }()

	fmt.Printf("platform: %s listening on %s\n", role, address)
	// A Passive instance is reachable too, and answers a different surface. Which
	// one it is serving is the thing an operator is asking about.
	observer.Record(ctx, APIListening{Address: address, InstanceState: api.InstanceStatePassive})

	return redundancy.Contend(ctx, lock, redundancy.Runtime{
		Passive: func(passiveCtx context.Context) error {
			return runPassive(passiveCtx, cfg, descriptor, role, statusPath, factory)
		},
		Active: func(activeCtx context.Context, kind redundancy.ActivationKind) error {
			return runActive(activeCtx, cfg, descriptor, role, statusPath, server, kind, factory)
		},
	})
}

// runPassive follows the journal while the machine's other instance is Active,
// and returns when this instance wins ownership or the process is stopping.
//
// It keeps its projection current so a takeover is quick, and it writes its
// status so deployment tooling can see whether this instance is ready to take
// over. It serves nothing: the listener is already up and answering the Passive
// surface, which needs none of this.
//
// A projection that will not open is not fatal here. An instance that cannot
// follow the journal must still be able to take ownership when the other one
// stops, so this retries until its context ends rather than giving up.
func runPassive(ctx context.Context, cfg *config.Config, descriptor config.Descriptor, role redundancy.InstanceRole, statusPath string, factory events.Factory) error {
	observer := operations.FromContext(ctx)
	site, err := openPassiveSite(ctx, cfg, descriptor, role, statusPath, factory)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		// Ownership was won, or the process is stopping, before a projection ever
		// opened. Either way there is nothing to run and nothing to close, and
		// neither is a failure.
		return nil
	}
	if err != nil {
		return err
	}

	statusCtx, stopStatus := context.WithCancel(context.WithoutCancel(ctx))
	defer stopStatus()
	statusDone, err := startStatus(statusCtx, site.fabric, role, redundancy.StatePassive, statusPath, cfg.LagBound(), nil, nil)
	if err != nil {
		return errors.Join(err, site.close(ctx), writeFailedStatus(statusPath, role, err))
	}

	fmt.Printf("platform: %s caught up and waiting for Primary Ownership\n", role)
	observer.Record(ctx, StandbyWaiting{})

	// Wait for ownership or for the process to stop. Either arrives as a canceled
	// context; which one it was is the ownership machine's business, not this
	// function's.
	<-ctx.Done()
	stopStatus()
	return errors.Join(<-statusDone, site.close(context.WithoutCancel(ctx)))
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
func openPassiveSite(ctx context.Context, cfg *config.Config, descriptor config.Descriptor, role redundancy.InstanceRole, statusPath string, factory events.Factory) (*site, error) {
	observer := operations.FromContext(ctx)
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		opened, err := open(ctx, descriptor, cfg, false, role, factory)
		if err == nil {
			return opened, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		fmt.Fprintf(os.Stderr, "platform: %s standby projection unavailable: %v; waiting for Primary Ownership\n", role, err)
		if attempt == 1 || attempt%10 == 0 {
			observer.Record(ctx, StandbyOpenRetry{Attempt: attempt, Error: err.Error()})
		}
		if writeErr := writeUnavailableStatus(statusPath, role, err); writeErr != nil {
			return nil, errors.Join(err, writeErr)
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
func runActive(ctx context.Context, cfg *config.Config, descriptor config.Descriptor, role redundancy.InstanceRole, statusPath string, server *instanceServer, kind redundancy.ActivationKind, factory events.Factory) error {
	observer := operations.FromContext(ctx)
	if err := writeTransitionStatus(statusPath, role, redundancy.StateActivating); err != nil {
		return err
	}
	fmt.Printf("platform: %s started for %s\n", kind, role)

	site, err := open(ctx, descriptor, cfg, true, role, factory)
	if err != nil {
		return errors.Join(err, writeFailedStatus(statusPath, role, err))
	}

	// A projection that falls too far behind stops serving rather than answering
	// from a stale view: the status loop cancels serving when it crosses the bound.
	serveCtx, stopServing := context.WithCancel(ctx)
	defer stopServing()
	statusCtx, stopStatus := context.WithCancel(context.WithoutCancel(ctx))
	var lagEvent sync.Once
	onLagExceeded := func() {
		lagEvent.Do(func() {
			observer.Record(ctx, ProjectionLagExceeded{LagBound: cfg.LagBound().String()})
		})
		stopServing()
	}
	onStatusFailure := func() {
		observer.Record(ctx, StatusWriteFailed{Path: statusPath})
		stopServing()
	}
	statusDone, err := startStatus(statusCtx, site.fabric, role, redundancy.StateActive, statusPath, cfg.LagBound(), onLagExceeded, onStatusFailure)
	if err != nil {
		stopStatus()
		return errors.Join(err, site.close(ctx), writeFailedStatus(statusPath, role, err))
	}
	statusStopped := false
	stopActiveStatus := func() error {
		if statusStopped {
			return nil
		}
		statusStopped = true
		stopStatus()
		return <-statusDone
	}

	address := instanceOf(descriptor, role).APIAddress
	server.serveWith(httpapi.NewHandler(site.commands, site.queries, func() api.Instance {
		return instanceIdentity(descriptor, role, api.InstanceStateActive)
	},
		// exposeSpec is false: the authoritative OpenAPI artifact is
		// api-specifications/openapi.yaml in git, not an endpoint on the runtime.
		false))
	fmt.Printf("platform: %s active, serving on %s\n", role, address)
	observer.Record(ctx, APIActive{Address: address, InstanceState: api.InstanceStateActive})

	serveErr := awaitStop(serveCtx, site, server)

	// Reverse of startup: HTTP intake stops and in-flight requests drain before
	// anything they could be holding is closed. Only then does the site release.
	transitionErr := errors.Join(stopActiveStatus(), writeTransitionStatus(statusPath, role, redundancy.StateStopping))
	shutdownErr := server.shutdown(cfg.ShutdownTimeout())
	closeErr := site.close(context.WithoutCancel(ctx))

	err = errors.Join(serveErr, transitionErr, shutdownErr, closeErr)
	stopped := APIStopped{}
	if err != nil {
		stopped.Error = err.Error()
	}
	observer.Record(ctx, stopped)
	if err != nil {
		return errors.Join(err, writeFailedStatus(statusPath, role, err))
	}
	return nil
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

type statusFabric interface {
	State(context.Context) (eventfabric.State, error)
}

// startStatus writes the initial status synchronously, then periodically writes
// live state until ctx ends. A write failure stops the runtime because deployment
// tooling must not act on a stale file during handover or machine shutdown.
func startStatus(ctx context.Context, fabric statusFabric, role redundancy.InstanceRole, state redundancy.State, statusPath string, lagBound time.Duration, onLagExceeded, onFailure func()) (<-chan error, error) {
	var lag redundancy.LagState
	write := func() error {
		now := time.Now()
		status := redundancy.Status{Role: role, State: state, PID: os.Getpid(), UpdatedAt: now.UTC(), FailoverReady: true}
		st, err := fabric.State(ctx)
		behind := lag.Observe(err != nil || !st.CaughtUp, now)
		status.Lag = behind.String()
		if err != nil {
			status.LastError = err.Error()
			status.FailoverReady = false
		} else {
			status.Applied, status.HighWater = st.Applied, st.HighWater
		}
		if redundancy.Exceeds(behind, lagBound) {
			status.FailoverReady = false
			if onLagExceeded != nil {
				onLagExceeded()
			}
		}
		return status.Write(statusPath)
	}

	if err := write(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(statusInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				done <- nil
				return
			case <-ticker.C:
				if err := write(); err != nil {
					if onFailure != nil {
						onFailure()
					}
					done <- err
					return
				}
			}
		}
	}()
	return done, nil
}

// writeFailedStatus records why a process stopped.
func writeFailedStatus(statusPath string, role redundancy.InstanceRole, cause error) error {
	return redundancy.Status{
		Role:      role,
		State:     redundancy.StateFailed,
		PID:       os.Getpid(),
		UpdatedAt: time.Now().UTC(),
		LastError: cause.Error(),
	}.Write(statusPath)
}

func writeTransitionStatus(statusPath string, role redundancy.InstanceRole, state redundancy.State) error {
	return redundancy.Status{
		Role:      role,
		State:     state,
		PID:       os.Getpid(),
		Lag:       unknownLag,
		UpdatedAt: time.Now().UTC(),
	}.Write(statusPath)
}

func writeUnavailableStatus(statusPath string, role redundancy.InstanceRole, cause error) error {
	return redundancy.Status{
		Role:      role,
		State:     redundancy.StatePassive,
		PID:       os.Getpid(),
		Lag:       unknownLag,
		UpdatedAt: time.Now().UTC(),
		LastError: cause.Error(),
	}.Write(statusPath)
}
