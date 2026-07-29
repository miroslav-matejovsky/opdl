package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/httpapi"
	"github.com/miroslav-matejovsky/opdl/platform/internal/instance/state"
	"github.com/miroslav-matejovsky/opdl/platform/internal/machine/redundancy"
)

// process is everything one running instance was composed from. It is assembled
// once in Run and passed down, so no function below reaches for configuration,
// identity, or storage on its own.
type process struct {
	descriptor config.Descriptor
	cfg        *config.Config
	role       redundancy.InstanceRole
	// started is when this process began running. It is the origin the health
	// endpoints report uptime against, so every instance answers uptime from one
	// point taken before it opened anything, in every state it later serves.
	started time.Time
	// local is the process-local publisher: the instance's own record, and the
	// machine's shared store for the machine-scoped facts in the same flow. It is
	// what composition, ownership, and the transport adapter state through, and
	// the transport adapter must never describe itself through itself.
	//
	// Sorting by scope happens inside it, in eventstore.Backend, rather than by
	// a caller choosing a publisher. A caller states what happened; where a fact
	// belongs is the fact's own property.
	local events.Publisher
	// state is this instance's durable state file, carrying the epoch counter
	// across restarts and crashes. Run advanced it once for this process; the
	// active composition advances it again each time the instance takes ownership.
	// There is nothing to close: every write is complete when it returns.
	state *state.Store
	// fabricHealth probes this instance's event fabric for the health endpoint:
	// it round-trips a message through the embedded broker and reports what
	// happened. It is the site client's Check, held as a func so nothing below
	// the composition root takes the client itself and starts using it for
	// something other than saying whether it works.
	//
	// It is set for every running process, because an instance starts its broker
	// and connects to it before it runs anything. It is nil only in a test that
	// composes a process without one.
	fabricHealth func(context.Context) error
	// serviceHealth renders this instance's view of the site's deployed services
	// for GET /health/services. It is held as a func for the same reason
	// fabricHealth is: nothing below the composition root should take the
	// monitoring subsystem itself and start using it for something other than
	// answering what it currently knows.
	//
	// It is set for every running process, because service monitoring starts
	// before the runtime does. It is nil only in a test that composes a process
	// without it, and an instance with none answers an empty view.
	serviceHealth func() api.ServiceHealthResponse
	// log is this process's application log, already stamped with the machine and
	// the instance role. It is what the runtime says things through; what it
	// states goes through local. A record here is for a person reading a failure,
	// so nothing routes, consumes, or asserts on one.
	//
	// The process owns the open file behind it and closes it; this is a view on
	// it. See internal/instance/applog.
	log *slog.Logger
}

// instanceOf returns the running instance's own record: the endpoint it binds and
// the directory it writes.
//
// Everything a single instance binds or writes is resolved onto that instance's
// record at build time, so this is where it is read from. A machine's two
// instances share one descriptor, so anything read from it without a role is a
// value they would both take.
func instanceOf(descriptor config.Descriptor, role redundancy.InstanceRole) config.Instance {
	return descriptor.Instance(config.Role(role == redundancy.RoleStandby))
}

// peerOf returns the machine's other instance's record. It is the zero record on
// a machine that deploys only a Primary Instance.
func peerOf(descriptor config.Descriptor, role redundancy.InstanceRole) config.Instance {
	return descriptor.Instance(config.Role(role != redundancy.RoleStandby))
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

// advanceEpoch begins a new incarnation of this instance and states it.
//
// Both the advance and the statement of it are returned on failure. An instance
// whose epoch could not be recorded must not carry on as though it had one: a
// later reader that used it as a fencing token would be ordering against a
// number no restart will reproduce. Run makes the process's own first advance
// directly, because it happens before there is a process value to pass.
func advanceEpoch(ctx context.Context, proc process, reason state.Reason) error {
	st, err := proc.state.Advance(reason)
	if err != nil {
		return errors.Join(err, proc.local.Publish(ctx, EpochAdvanceFailed{
			StateFile: proc.state.Path(),
			Reason:    string(reason),
			Error:     err.Error(),
		}))
	}
	return proc.local.Publish(ctx, epochAdvanced(proc.state.Path(), reason, st))
}

// epochAdvanced describes a recorded advance. It is shared with Run, which makes
// the process's own first advance before there is a process value to pass, so
// both statements of the same fact carry the same fields.
func epochAdvanced(stateFile string, reason state.Reason, st state.State) EpochAdvanced {
	return EpochAdvanced{
		StateFile:       stateFile,
		Epoch:           st.Epoch,
		Reason:          string(reason),
		ProcessEpoch:    st.ProcessEpoch.Count,
		ActivationEpoch: st.ActivationEpoch.Count,
	}
}

// instanceIdentity describes this instance to its own API in the given state.
//
// Every field comes from the descriptor the process was compiled with, so it is
// answerable from the moment the process starts and in every state it later
// serves. That is what makes a Passive instance worth asking.
func instanceIdentity(descriptor config.Descriptor, role redundancy.InstanceRole, stateName string) api.Instance {
	return api.Instance{
		Machine:     descriptor.Machine,
		Role:        string(role),
		State:       stateName,
		Address:     instanceOf(descriptor, role).APIAddress,
		PeerAddress: peerOf(descriptor, role).APIAddress,
	}
}

// apiDeps is what this process's API answers from, in the given runtime state.
//
// Both of an instance's surfaces are built from it, and both are built from the
// same one: an instance answers the same health and the same identity whether it
// is Active or Passive, and the only thing that differs is what it says its
// state is. Composing the two handlers from one place is what keeps that true —
// a surface that quietly stopped reporting the service view or the lease would
// otherwise be a difference an operator only finds after ownership has moved.
func apiDeps(proc process, lease *redundancy.Lease, stateName string) api.Deps {
	return api.Deps{
		Instance: func() api.Instance {
			return instanceIdentity(proc.descriptor, proc.role, stateName)
		},
		Started:       proc.started,
		Lease:         func() api.LeaseView { return leaseViewOf(lease) },
		EventFabric:   proc.fabricHealth,
		ServiceHealth: proc.serviceHealth,
	}
}

// runProcess binds this instance's API, opens the machine's ownership lease, and
// runs the passive and active compositions as ownership moves.
//
// The order is deliberate. The listener opens first and stays open for the whole
// process, so an instance is reachable in every state and a bind failure stops it
// at startup rather than at a failover. Ownership decides only what it answers,
// and it can decide differently many times over one process's life: a step-down
// or an automatic failback swaps the Passive surface back in place rather than
// stopping the process.
func runProcess(ctx context.Context, proc process) (runErr error) {
	descriptor, role := proc.descriptor, proc.role

	leaseCfg, err := leaseConfigOf(descriptor)
	if err != nil {
		return errors.Join(err, proc.local.Publish(ctx, LeaseOpenFailed{File: leaseCfg.File, Error: err.Error()}))
	}
	lease, err := redundancy.OpenLease(leaseCfg, role)
	if err != nil {
		return errors.Join(err, proc.local.Publish(ctx, LeaseOpenFailed{File: leaseCfg.File, Error: err.Error()}))
	}
	// The lease is a file, not a kernel handle, so there is nothing to close: a
	// process that leaves releases it inside the active composition, and a process
	// that dies lets its grant lapse, which is what a promoter waits out.

	address := instanceOf(descriptor, role).APIAddress
	passive := httpapi.NewPassiveHandler(apiDeps(proc, lease, api.InstanceStatePassive))
	standby := role == redundancy.RoleStandby
	server, err := openInstanceServer(ctx, address, proc.cfg.ReadHeaderTimeout(standby), passive)
	if err != nil {
		return errors.Join(err, proc.local.Publish(ctx, APIListenFailed{Address: address, Error: err.Error()}))
	}
	// Whatever else happens, the listener drains before the process leaves.
	defer func() { runErr = errors.Join(runErr, server.shutdown(proc.cfg.ShutdownTimeout(standby))) }()

	proc.log.Info("listening", "address", address, "instance_state", api.InstanceStatePassive)
	// A Passive instance is reachable too, and answers a different surface. Which
	// one it is serving is the thing an operator is asking about.
	if err := proc.local.Publish(ctx, APIListening{Address: address, InstanceState: api.InstanceStatePassive}); err != nil {
		return err
	}

	// The health gate a Passive instance promotes through: it takes ownership only
	// when the lease has lapsed and the machine's other instance no longer answers
	// its health endpoint. A machine with no peer has none to check.
	deps := redundancy.Deps{PeerHealthy: peerHealthCheck(proc.cfg, peerOf(descriptor, role).APIAddress)}

	return redundancy.ManageOwnership(ctx, proc.local, lease, deps, redundancy.Runtime{
		Passive: func(passiveCtx context.Context) error {
			return runPassive(passiveCtx, proc)
		},
		Active: func(activeCtx context.Context, kind redundancy.ActivationKind) error {
			return runActive(activeCtx, proc, server, lease, passive, kind)
		},
	})
}

// runPassive waits while the machine's other instance is Active, and returns
// when this instance wins ownership or the process is stopping.
//
// It serves nothing and follows nothing. The listener is already up and
// answering the Passive surface, which needs none of this, and the platform
// distributes no state for a Passive instance to trail: taking over costs it no
// catch-up, so there is no readiness to report beyond being here.
func runPassive(ctx context.Context, proc process) error {
	proc.log.Info("waiting for Primary Ownership")
	if err := proc.local.Publish(ctx, StandbyWaiting{}); err != nil {
		return err
	}
	// Wait for ownership or for the process to stop. Either arrives as a canceled
	// context; which one it was is the ownership machine's business, not this
	// function's.
	<-ctx.Done()
	return nil
}

// runActive swaps this instance's Active surface onto the listener and serves it
// until signaled or until the listener dies.
//
// It does not open a listener. One is already bound and answering the Passive
// surface, so activation swaps the handler rather than moving the endpoint, and
// the address a caller uses never changes.
func runActive(ctx context.Context, proc process, server *instanceServer, lease *redundancy.Lease, passive http.Handler, kind redundancy.ActivationKind) error {
	cfg, descriptor, role := proc.cfg, proc.descriptor, proc.role
	// That this instance is activating was stated by the ownership machine before
	// it called this, so there is no fact to state here that is not already in the
	// record. The log record is not that fact: it is so a reader of this process's
	// log sees the activation without opening the event record beside it.
	proc.log.Info("activating", "activation", string(kind))

	// Becoming Active is a new incarnation: from here this instance produces
	// decisions and writes on the machine's behalf, and anything it writes must be
	// distinguishable from what the previous holder of ownership wrote. The epoch
	// advances before the instance serves anything, and a failure to record it
	// stops the activation rather than letting it serve under an epoch nothing
	// persisted.
	if err := advanceEpoch(ctx, proc, state.ReasonActivated); err != nil {
		return err
	}

	address := instanceOf(descriptor, role).APIAddress
	server.serveWith(httpapi.NewActiveHandler(apiDeps(proc, lease, api.InstanceStateActive)))
	proc.log.Info("active", "address", address, "instance_state", api.InstanceStateActive)

	// An instance that cannot state that it is serving does not stay serving. The
	// failure takes the place of the reason it would otherwise have stopped, and
	// the ordered shutdown below runs on it exactly as it does on a signal.
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

	// Reverse of startup: the Passive surface takes the listener back at once, so
	// new requests stop reaching the Active handler, and the requests already
	// running against it drain. The listener itself stays bound — this instance
	// may be stepping down rather than stopping, and if the process is stopping,
	// runProcess's deferred shutdown closes it afterwards.
	server.serveWith(passive)
	drainErr := server.drain(cfg.ShutdownTimeout(role == redundancy.RoleStandby))

	err := errors.Join(serveErr, drainErr)
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
