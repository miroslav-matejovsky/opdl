package redundancy

import (
	"context"
	"errors"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// This file is ownership: what holding the lease means at runtime, the order the
// two states are entered and left in, and how promotion is decided. The lease
// itself — the record and the file operations on it — is lease.go.
//
// The composition package supplies two functions, one to run while Passive and
// one while Active, and Contend decides when each runs. Passive has returned
// before Active is called, and ownership is released only after Active has
// returned, so an instance's active resources are opened only after its passive
// ones have closed and only while it holds the lease.

// Acquisition is the outcome of contending for Primary Ownership.
type Acquisition struct {
	// Held reports whether this process now holds Primary Ownership.
	Held bool
	// Abandoned reports that ownership was taken over from a process whose lease
	// lapsed without a clean release, rather than from one that handed it over. It
	// is only meaningful when Held is true, and it is the operational distinction
	// the non-expiring mutex this replaced could not make at all.
	Abandoned bool
}

// ActivationKind is why an instance became Active. It is derived here rather than
// by the caller, because the reason is a fact about how ownership was obtained.
type ActivationKind string

const (
	// ActivationInitial is an instance that took ownership uncontested at startup.
	ActivationInitial ActivationKind = "initial activation"
	// ActivationFailover is a Standby Instance that took ownership from the
	// Primary Instance.
	ActivationFailover ActivationKind = "failover"
	// ActivationFailback is a Primary Instance that took ownership back from the
	// Standby Instance.
	ActivationFailback ActivationKind = "failback"
)

// Runtime is the pair of compositions an instance moves between. Contend runs at
// most one of them at a time, and never both.
//
// The two are separated by ownership: Passive must have returned before Active is
// called, so an instance's active resources are opened only after its passive
// ones have closed and only while it holds the lease.
type Runtime struct {
	// Passive runs while the machine's other instance holds ownership. Its context
	// is canceled the moment this instance wins ownership, and it must return when
	// that happens: Active does not start until it has.
	Passive func(ctx context.Context) error
	// Active runs while this instance holds ownership. Returning ends the
	// instance's turn, and ownership is released once it has. Its context is also
	// canceled if the renewal loop cannot keep the lease alive, which is how an
	// instance that can no longer renew stops being Active before its lease lapses.
	Active func(ctx context.Context, kind ActivationKind) error
}

// Deps are the runtime capabilities the ownership machine needs but does not own.
// The composition package supplies them, so this package makes no network call
// and reaches for no clock but time.Now.
type Deps struct {
	// PeerHealthy reports whether the machine's other instance answers its health
	// endpoint as able to serve. A Passive instance promotes only when the lease
	// has lapsed and this returns false: a lapsed lease alone is not enough, so a
	// momentarily slow-to-renew but healthy Active is not failed over.
	//
	// It is nil on a machine with no peer to check, where it is never consulted.
	PeerHealthy func(ctx context.Context) bool
}

// Contend drives one instance through its whole ownership lifecycle.
//
// An instance that finds the lease free at startup takes it and goes straight to
// Active. One that finds it held runs Passive and promotes when the lease lapses
// and its peer is unhealthy. A machine that deploys no standby has no lease, so
// lease is nil, acquiring always succeeds, and Passive is never reached.
//
// Ownership is released after Active returns and before Contend does, so the other
// instance cannot start composing its active resources while this one is still
// closing its own.
//
// publisher is how this package states what it did. Every statement on the startup
// path is returned on failure, so a failure to state one stops the instance rather
// than leaving a machine whose ownership moved with no record that it did.
func Contend(ctx context.Context, publisher events.Publisher, lease *Lease, deps Deps, runtime Runtime) error {
	if publisher == nil {
		return errors.New("redundancy: publisher is required")
	}

	// A machine with no standby has no lease and nothing to contend for: it is
	// Active by construction, so there was no ownership to take from anyone.
	if lease == nil {
		return activate(ctx, publisher, nil, deps, runtime, ActivationInitial)
	}

	if err := publisher.Publish(ctx, LeaseOpened{File: lease.File()}); err != nil {
		return err
	}

	acquired, err := lease.tryAcquire(time.Now())
	if err != nil {
		return err
	}
	if acquired.Held {
		if err := publisher.Publish(ctx, OwnershipAcquired{File: lease.File(), Abandoned: acquired.Abandoned}); err != nil {
			return err
		}
		return activate(ctx, publisher, lease, deps, runtime, ActivationInitial)
	}

	if err := publisher.Publish(ctx, OwnershipWaiting{File: lease.File()}); err != nil {
		return err
	}

	acquired, err = waitWhilePassive(ctx, publisher, lease, deps, runtime)
	if err != nil || !acquired.Held {
		return err
	}
	if err := publisher.Publish(ctx, OwnershipAcquired{File: lease.File(), Abandoned: acquired.Abandoned}); err != nil {
		return err
	}
	return activate(ctx, publisher, lease, deps, runtime, activationKind(lease.Role()))
}

// activationKind names why this instance is taking over after waiting. A Standby
// Instance winning ownership is a failover; the Primary Instance winning it back
// is a failback.
func activationKind(role InstanceRole) ActivationKind {
	if role == RolePrimary {
		return ActivationFailback
	}
	return ActivationFailover
}

// waitWhilePassive runs the Passive composition and evaluates promotion on a
// timer until this instance wins ownership or ctx ends.
//
// Each tick it reads the lease. While the other instance's grant is valid it stays
// Passive. Once the grant has lapsed it consults the peer's health, and promotes
// only if the peer is unhealthy — the two conditions the draft requires. Waiting
// for Passive to return before reporting the acquisition is what guarantees the
// instance's passive resources are closed before its active ones open.
func waitWhilePassive(ctx context.Context, publisher events.Publisher, lease *Lease, deps Deps, runtime Runtime) (Acquisition, error) {
	passiveCtx, stopPassive := context.WithCancel(ctx)
	defer stopPassive()

	passiveErr := make(chan error, 1)
	go func() { passiveErr <- runtime.Passive(passiveCtx) }()

	ticker := time.NewTicker(lease.cfg.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Asked to stop while waiting. Not a failure; drain Passive and leave.
			stopPassive()
			<-passiveErr
			return Acquisition{}, nil
		case err := <-passiveErr:
			// Passive returned on its own, meaning it can no longer follow the
			// journal. Stop waiting rather than staying parked able to win ownership
			// it is not ready to use.
			return Acquisition{}, err
		case <-ticker.C:
			acquired, err := evaluatePromotion(ctx, publisher, lease, deps)
			if err != nil {
				// A transient read of the shared file. Report nothing and retry;
				// the peer's grant has not changed because we failed to read it.
				continue
			}
			if !acquired.Held {
				continue
			}
			stopPassive()
			if perr := <-passiveErr; perr != nil {
				return Acquisition{}, perr
			}
			return acquired, nil
		}
	}
}

// evaluatePromotion decides whether this Passive instance may take ownership now.
// It promotes only when the lease has lapsed and the peer is unhealthy, and
// states a declined evaluation when the lease is free but the peer still answers,
// which is what failover troubleshooting reads.
func evaluatePromotion(ctx context.Context, publisher events.Publisher, lease *Lease, deps Deps) (Acquisition, error) {
	free, err := lease.free(time.Now())
	if err != nil {
		return Acquisition{}, err
	}
	if !free {
		return Acquisition{}, nil // the other instance's grant is still valid
	}
	if deps.PeerHealthy != nil && deps.PeerHealthy(ctx) {
		// The lease lapsed but the peer still serves. Promoting now could leave two
		// Active instances, so decline and keep watching.
		events.BestEffort(publisher).State(ctx, PromotionDeclined{Reason: "peer is healthy"})
		return Acquisition{}, nil
	}
	return lease.tryAcquire(time.Now())
}

// activate runs the Active composition, keeps its lease renewed for as long as it
// runs, and releases ownership once it returns.
//
// The renewal loop shares the Active composition's fate in both directions: while
// Active serves, the loop extends the lease; if the loop cannot keep the lease
// alive, it cancels serving so this instance stops being Active before a promoter
// could see the lease lapsed. Release happens after Active has returned, which is
// the ordering the other instance depends on.
//
// An Active Standby also runs a failback watcher: once the returning Primary has
// been healthy for the stabilization window, it stops serving and releases, so
// the preferred Primary reclaims ownership. See startFailback.
func activate(ctx context.Context, publisher events.Publisher, lease *Lease, deps Deps, runtime Runtime, kind ActivationKind) error {
	started := time.Now()
	if err := publisher.Publish(ctx, ActivationStarted{Kind: kind}); err != nil {
		return err
	}

	activeCtx, stopActive := context.WithCancel(ctx)
	defer stopActive()

	var renewalDone, failbackDone <-chan struct{}
	if lease != nil {
		renewalDone = startRenewal(activeCtx, publisher, lease, stopActive)
		if lease.Role() == RoleStandby && deps.PeerHealthy != nil && lease.cfg.FailbackStabilization > 0 {
			failbackDone = startFailback(activeCtx, publisher, lease, deps, stopActive)
		}
	}

	err := runtime.Active(activeCtx, kind)
	stopActive()
	if renewalDone != nil {
		<-renewalDone
	}
	if failbackDone != nil {
		<-failbackDone
	}
	releaseErr := lease.release()

	elapsed := time.Since(started).Milliseconds()
	if err != nil {
		stateErr := publisher.Publish(ctx, ActivationFailed{Kind: kind, DurationMS: elapsed, Error: err.Error()})
		return errors.Join(err, releaseErr, stateErr)
	}
	return errors.Join(publisher.Publish(ctx, ActivationCompleted{Kind: kind, DurationMS: elapsed}), releaseErr)
}

// startFailback hands ownership back to a returning healthy Primary.
//
// It runs only on an Active Standby: an instance that took over after the Primary
// failed. Under the Preferred Primary policy the Primary should end up Active, so
// once its health endpoint has reported it able to serve for an uninterrupted
// FailbackStabilization window, this Standby steps down — it stops serving, which
// leads activate to release the lease, and the process leaves so its service
// manager restarts it Passive while the Primary reclaims ownership.
//
// The stabilization window resets the moment the Primary looks unhealthy again, so
// a Primary that is only intermittently reachable does not trigger a handover that
// would immediately fail back the other way. Failback is automatic; there is no
// manual mode.
//
// Stepping down by exiting mirrors the renewal loop's step-down. Rejoining as
// Passive in place, without the restart, is the same refactor that item needs;
// see docs/plans/redundancy-rest.md.
func startFailback(ctx context.Context, publisher events.Publisher, lease *Lease, deps Deps, stepDown func()) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(lease.cfg.HealthCheckInterval)
		defer ticker.Stop()
		var healthySince time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !deps.PeerHealthy(ctx) {
					healthySince = time.Time{} // the Primary is not back yet; reset the window
					continue
				}
				if healthySince.IsZero() {
					healthySince = time.Now()
				}
				if time.Since(healthySince) >= lease.cfg.FailbackStabilization {
					events.BestEffort(publisher).State(ctx, FailbackInitiated{})
					stepDown()
					return
				}
			}
		}
	}()
	return done
}

// startRenewal extends the lease every renewal interval for as long as ctx runs,
// and steps the instance down if it can no longer keep the lease alive.
//
// A renewal that finds the lease no longer names this instance means another
// instance took over, and this one steps down at once. A renewal that fails to
// read or write the shared file is retried, until the lease is within one renewal
// interval of expiry — the step-down grace — at which point this instance stops
// being Active so a promoter never sees the lease lapsed while it still serves.
//
// Stepping down cancels serving through onLost. The instance then releases and
// Contend returns; the process leaves and its service manager restarts it, which
// is the conservative choice this pass makes over rejoining as Passive in place
// (see docs/plans/redundancy-rest.md).
func startRenewal(ctx context.Context, publisher events.Publisher, lease *Lease, onLost func()) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(lease.cfg.RenewalInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				err := lease.renew(time.Now())
				if err == nil {
					continue
				}
				if errors.Is(err, errOwnershipLost) {
					events.BestEffort(publisher).State(ctx, SteppedDown{Reason: "ownership was taken over"})
					onLost()
					return
				}
				// A transient failure to renew. Keep trying until the lease is close
				// enough to expiry that staying Active is no longer safe.
				if time.Now().After(lease.ownedExpiry().Add(-lease.cfg.RenewalInterval)) {
					events.BestEffort(publisher).State(ctx, SteppedDown{Reason: "could not renew the lease before it expired: " + err.Error()})
					onLost()
					return
				}
				events.BestEffort(publisher).State(ctx, LeaseRenewalFailed{Error: err.Error()})
			}
		}
	}()
	return done
}
