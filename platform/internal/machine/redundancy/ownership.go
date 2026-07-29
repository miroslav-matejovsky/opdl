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
// one while Active, and ManageOwnership decides when each runs. Passive has
// returned before Active is called, and ownership is released only after Active
// has returned, so an instance's active resources are opened only after its
// passive ones have closed and only while it holds the lease.

// The bounded reasons a Passive instance declines a promotion it could have
// taken. They are what PromotionDeclined carries, so an operator reading one
// instance's record can tell why the machine did not change hands.
const (
	// reasonPeerHealthy is a promotable grant left alone because the other
	// instance still answers: a slow owner is not failed over.
	reasonPeerHealthy = "peer is healthy"
	// reasonHandover is this instance's own released grant, still inside the
	// handover window.
	//
	// An instance that hands ownership over writes a released grant that still
	// names itself, so until the peer notices and claims it, the lease file
	// describes a grant that is promotable to its own former owner. The only
	// thing between that instance and taking straight back what it just gave
	// away is one health probe against a peer that is, right then, in the middle
	// of activating — and a probe that misses because a connection was refused,
	// a keep-alive was dropped, or a loopback GET outran its timeout on a loaded
	// host is not evidence that the peer cannot serve.
	//
	// The window is the lease Duration, which is deliberately not a new timing
	// knob. It costs nothing on every ordinary failback, where the peer claims
	// the grant within a health check interval and this instance never reaches
	// the window at all. It costs a bounded delay in the one case that matters,
	// a peer that really did die between being handed ownership and taking it —
	// and that delay is the same Duration the machine already waits before
	// acting on any owner that stopped renewing, so its worst-case time without
	// an Active instance is unchanged.
	reasonHandover = "handover in progress"
)

// Acquisition is the outcome of one attempt to take Primary Ownership.
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
	// ActivationInitial is an instance that took a free lease at startup, with
	// nobody to take it from.
	ActivationInitial ActivationKind = "initial activation"
	// ActivationFailover is a Standby Instance that took ownership from the
	// Primary Instance.
	ActivationFailover ActivationKind = "failover"
	// ActivationFailback is a Primary Instance that took ownership back from the
	// Standby Instance.
	ActivationFailback ActivationKind = "failback"
)

// Runtime is the pair of compositions an instance moves between. ManageOwnership
// runs at most one of them at a time, and never both.
//
// The two are separated by ownership: Passive must have returned before Active is
// called, so an instance's active resources are opened only after its passive
// ones have closed and only while it holds the lease. Both are re-entered as
// ownership moves over the process's lifetime, so neither may assume it runs
// once.
type Runtime struct {
	// Passive runs while the machine's other instance holds ownership. Its context
	// is canceled the moment this instance wins ownership, and it must return when
	// that happens: Active does not start until it has.
	Passive func(ctx context.Context) error
	// Active runs while this instance holds ownership. Returning ends the
	// instance's turn: the lease is released and, unless the process is stopping,
	// the instance re-enters the Passive state in place. Its context is also
	// canceled when the instance must stop being Active — a lease that cannot be
	// renewed, or an automatic failback to the returning Primary.
	Active func(ctx context.Context, kind ActivationKind) error
}

// Deps are the runtime capabilities the ownership machine needs but does not own.
// The composition package supplies them, so this package makes no network call
// and reaches for no clock but time.Now.
type Deps struct {
	// PeerHealthy reports whether the machine's other instance answers its health
	// endpoint as able to serve. A Passive instance promotes onto a lapsed lease
	// only when this returns false, so a momentarily slow-to-renew but healthy
	// Active is not failed over; an Active Standby fails back to the Primary once
	// this has returned true for the whole stabilization window.
	//
	// It is nil on a machine with no peer to check, where a lapsed lease alone
	// permits promotion and failback never runs.
	PeerHealthy func(ctx context.Context) bool
}

// ManageOwnership drives one instance's whole ownership lifecycle, for the life
// of the process: Passive and Active turns alternate in place, and the function
// returns only when ctx ends or a composition fails.
//
// An instance that finds the lease free at startup takes it and goes straight to
// Active. One that finds it held runs Passive and promotes when the lease is
// handed over or lapses with the peer unhealthy. An Active instance that steps
// down — a failback to the returning Primary, or a lease it could not keep —
// releases and re-enters Passive without the process exiting, so a Passive
// instance keeps answering its health endpoints. A machine that deploys no
// standby has no lease, so lease is nil and the instance is Active by
// construction for as long as it runs.
//
// Ownership is released after Active returns and before the next Passive turn
// begins, so the other instance cannot start composing its active resources
// while this one is still closing its own.
//
// publisher is how this package states what it did. Every statement on the
// turn-taking path is returned on failure, so a failure to state one stops the
// instance rather than leaving a machine whose ownership moved with no record
// that it did.
func ManageOwnership(ctx context.Context, publisher events.Publisher, lease *Lease, deps Deps, runtime Runtime) error {
	if publisher == nil {
		return errors.New("redundancy: publisher is required")
	}

	// A machine with no standby has no lease and no turns to take: it is Active by
	// construction, so there was no ownership to take from anyone and nobody to
	// hand it to when Active returns.
	if lease == nil {
		return activate(ctx, publisher, nil, deps, runtime, ActivationInitial)
	}

	if err := publisher.Publish(ctx, LeaseOpened{File: lease.File()}); err != nil {
		return err
	}

	firstTurn := true
	for {
		if ctx.Err() != nil {
			return nil
		}

		kind := activationKind(lease.Role())
		acquired := Acquisition{}
		if firstTurn {
			// At startup a free lease is taken at once: nothing has served yet, so
			// there is no peer state to weigh and no reason to wait a tick.
			var err error
			acquired, err = lease.tryAcquire(time.Now())
			if err != nil {
				return err
			}
			if acquired.Held {
				kind = ActivationInitial
			}
		}
		firstTurn = false

		if !acquired.Held {
			if err := publisher.Publish(ctx, OwnershipWaiting{File: lease.File()}); err != nil {
				return err
			}
			var err error
			acquired, err = waitWhilePassive(ctx, publisher, lease, deps, runtime)
			if err != nil {
				return err
			}
			if !acquired.Held {
				return nil // the process is stopping; it never won a turn
			}
		}

		if err := publisher.Publish(ctx, OwnershipAcquired{File: lease.File(), Abandoned: acquired.Abandoned}); err != nil {
			return err
		}
		if err := activate(ctx, publisher, lease, deps, runtime, kind); err != nil {
			return err
		}
		// Active ended without error: either the process is stopping, which the
		// top of the loop answers, or this instance stepped down — a failback, a
		// lease it could not keep, or a composition that stopped serving — and its
		// next turn starts Passive.
	}
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
// Each tick it reads the lease and decides through evaluatePromotion. Waiting
// for Passive to return before reporting the acquisition is what guarantees the
// instance's passive resources are closed before its active ones open.
func waitWhilePassive(ctx context.Context, publisher events.Publisher, lease *Lease, deps Deps, runtime Runtime) (Acquisition, error) {
	passiveCtx, stopPassive := context.WithCancel(ctx)
	defer stopPassive()

	passiveErr := make(chan error, 1)
	go func() { passiveErr <- runtime.Passive(passiveCtx) }()

	ticker := time.NewTicker(lease.cfg.HealthCheckInterval)
	defer ticker.Stop()

	// A declined promotion is stated once per stretch of the same reason rather
	// than on every tick, so a gated window does not grow the append-only record
	// by a line per interval.
	lastDecline := ""

	for {
		select {
		case <-ctx.Done():
			// Asked to stop while waiting. Not a failure; drain Passive and leave.
			stopPassive()
			<-passiveErr
			return Acquisition{}, nil
		case err := <-passiveErr:
			// Passive returned an error on its own, meaning it can no longer hold
			// up its side of being a standby. Stop waiting rather than staying
			// parked able to win ownership it is not ready to use.
			return Acquisition{}, err
		case <-ticker.C:
			acquired, decline, err := evaluatePromotion(ctx, lease, deps)
			if err != nil {
				// A transient read of the shared file. Report nothing and retry;
				// the peer's grant has not changed because we failed to read it.
				continue
			}
			if decline != "" && decline != lastDecline {
				events.BestEffort(publisher).State(ctx, PromotionDeclined{Reason: decline})
			}
			lastDecline = decline
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

// evaluatePromotion decides whether this Passive instance may take ownership now,
// from what the lease record says and, when it matters, the peer's health. It
// returns the acquisition outcome and, when promotion was possible but withheld,
// the reason it was declined.
//
// The rules, per lease state:
//
//   - held: the owner is serving; nothing to decide.
//   - absent: the machine's first start; take it, and let the write-then-confirm
//     in tryAcquire resolve a simultaneous first start to one owner.
//   - released by the other instance: an explicit handover — a graceful stop or
//     an automatic failback — addressed to this instance. Promote at once; the
//     peer's health is irrelevant because the owner said it was done.
//   - released by this instance, within the handover window: the peer has not
//     had time to claim what it was just handed. Decline; see reasonHandover.
//   - released by this instance, after it: the handover was not taken up.
//     Retake only if the peer cannot serve at all.
//   - lapsed, this instance's own grant: a step-down whose release did not land.
//     Reclaim; nobody else's claim is being overridden.
//   - lapsed, the other instance's grant: the owner stopped renewing. Promote
//     only if the peer is also unhealthy, so a slow-but-serving owner is not
//     failed over; a healthy owner reclaims its own lapsed grant by this same
//     rule from its side.
//
// One now is read for the whole decision, so what is observed and what is
// acquired are judged against the same instant.
func evaluatePromotion(ctx context.Context, lease *Lease, deps Deps) (Acquisition, string, error) {
	now := time.Now()
	avail, owner, err := lease.observe(now)
	if err != nil {
		return Acquisition{}, "", err
	}

	switch avail {
	case leaseHeld:
		return Acquisition{}, "", nil
	case leaseAbsent:
		acquired, err := lease.tryAcquire(now)
		return acquired, "", err
	case leaseReleased:
		if owner != lease.Role() {
			acquired, err := lease.tryAcquire(now)
			return acquired, "", err
		}
		if now.Sub(lease.handedOverAt()) < lease.cfg.Duration {
			return Acquisition{}, reasonHandover, nil
		}
	case leaseLapsed:
		if owner == lease.Role() {
			acquired, err := lease.tryAcquire(now)
			return acquired, "", err
		}
	}

	// A lease this instance released and the peer never took, or the other
	// instance's lapsed grant: both promote only when the peer cannot serve.
	if deps.PeerHealthy != nil && deps.PeerHealthy(ctx) {
		return Acquisition{}, reasonPeerHealthy, nil
	}
	acquired, err := lease.tryAcquire(now)
	return acquired, "", err
}

// activate runs the Active composition, keeps its lease renewed for as long as it
// runs, and releases ownership once it returns.
//
// The renewal loop shares the Active composition's fate in both directions: while
// Active serves, the loop extends the lease; if the loop cannot keep the lease
// alive, it cancels serving so this instance stops being Active before a promoter
// could see the lease lapsed. Release happens after Active has returned, which is
// the ordering the other instance depends on: the released record is what invites
// the peer to take over, so it must not be written while this instance's active
// resources are still open.
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
	releaseErr := lease.release(time.Now())

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
// FailbackStabilization window, this Standby steps down — it stops serving, its
// turn releases the lease, and it re-enters the Passive state in place while the
// Primary takes the released grant.
//
// The stabilization window resets the moment the Primary looks unhealthy again, so
// a Primary that is only intermittently reachable does not trigger a handover that
// would immediately fail back the other way. Failback is automatic; there is no
// manual mode.
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
// Stepping down cancels serving through onLost. The instance's turn then ends,
// its release is attempted, and it re-enters the Passive state in place; if the
// release could not be written either, its own lapsed grant is what it later
// reclaims from the Passive side once the file is writable again.
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
