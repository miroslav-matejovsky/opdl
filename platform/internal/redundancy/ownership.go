package redundancy

import (
	"context"
	"errors"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/internal/operations"
)

// This file is ownership: what holding the lock means at runtime, and the order
// the two states are entered and left in. The lock itself is lock.go.
//
// The state machine used to live in the runtime composition package, mixed in
// with opening an Event Fabric and serving HTTP. It is here because none of it is
// about what an instance runs: it is about which instance may run it, when the
// other one must have stopped, and what an operator is told about the move. The
// composition package now supplies two functions and this decides when each runs.

// Acquisition is the outcome of contending for Primary Ownership.
type Acquisition struct {
	// Held reports whether this process now holds Primary Ownership.
	Held bool
	// Abandoned reports that ownership was taken over from a process that died
	// without releasing it, rather than from one that handed it over cleanly. It
	// is only meaningful when Held is true.
	//
	// Ownership is equally valid either way: a dead process's listener, handlers,
	// and embedded server died with it. The distinction is operational, and it is
	// one the file lock this replaced could not make at all.
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
// The two are separated by ownership, which is the whole point: Passive must have
// returned before Active is called, so an instance's active resources are opened
// only after its passive ones have closed and only while it holds the lock.
type Runtime struct {
	// Passive runs while the machine's other instance holds ownership. Its context
	// is canceled the moment this instance wins ownership, and it must return when
	// that happens: Active does not start until it has.
	//
	// A Passive composition that cannot open is not fatal. It is expected to keep
	// trying until its context ends, because an instance that cannot follow the
	// journal yet must still be able to take over when asked.
	Passive func(ctx context.Context) error
	// Active runs while this instance holds ownership. Returning ends the
	// instance's turn, and ownership is released once it has.
	Active func(ctx context.Context, kind ActivationKind) error
}

// Contend drives one instance through its whole ownership lifecycle.
//
// An instance that takes the lock at startup goes straight to Active. One that
// does not runs Passive until the holder releases or dies, then activates. A
// machine that deploys no standby has no lock, so lock is nil, acquiring always
// succeeds, and Passive is never reached.
//
// The wait is a kernel wait, so a waiting instance is parked until the holder
// lets go rather than polling for it.
//
// Ownership is released after Active returns and before Contend does, so the
// other instance cannot start composing its active resources while this one is
// still closing its own.
func Contend(ctx context.Context, lock *Lock, runtime Runtime) error {
	observer := operations.FromContext(ctx)

	if lock != nil {
		observer.Emit("platform.lock_opened", operations.LevelInfo, "platform.redundancy", "ownership object opened", map[string]any{
			operations.AttributeObject: lock.Name(),
			// Whether a peer process on this machine already had the object open. Both
			// processes must report the same object; a machine whose two processes
			// report different ones was built from mismatched packages.
			operations.AttributeExisted: lock.Existed(),
		})
	}

	acquired, err := lock.TryAcquire()
	if err != nil {
		return err
	}
	if acquired.Held {
		emitAcquired(observer, lock, acquired)
		return activate(ctx, lock, runtime, ActivationInitial)
	}

	observer.Emit("platform.ownership_waiting", operations.LevelInfo, "platform.redundancy",
		"Primary Ownership is held by the other instance", map[string]any{operations.AttributeObject: lock.Name()})

	acquired, err = waitWhilePassive(ctx, lock, runtime)
	if err != nil || !acquired.Held {
		return err
	}
	emitAcquired(observer, lock, acquired)
	return activate(ctx, lock, runtime, activationKind(lock.Role()))
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

// waitWhilePassive runs the Passive composition until this instance wins
// ownership or ctx ends.
//
// The wait and the composition run together, and the composition's context is
// canceled as soon as ownership is won. Waiting for Passive to return before
// reporting the acquisition is what guarantees the instance's passive resources
// are closed before its active ones open: the two compositions open the same
// journal storage and the same node identity, so an overlap is a machine running
// two of itself.
func waitWhilePassive(ctx context.Context, lock *Lock, runtime Runtime) (Acquisition, error) {
	passiveCtx, stopPassive := context.WithCancel(ctx)
	defer stopPassive()

	type outcome struct {
		acquired Acquisition
		err      error
	}
	// The wait happens in the kernel, so this goroutine is parked until the holder
	// releases or dies rather than polling for it.
	won := make(chan outcome, 1)
	go func() {
		acquired, err := lock.Acquire(passiveCtx)
		// Ownership is this instance's from here, so the passive composition must
		// stop before anything else happens.
		stopPassive()
		won <- outcome{acquired: acquired, err: err}
	}()

	passiveErr := runtime.Passive(passiveCtx)
	// Passive returning on its own means the instance can no longer follow the
	// journal. It stops waiting rather than staying parked in a state where it
	// could win ownership it is not ready to use.
	stopPassive()
	result := <-won

	switch {
	case passiveErr != nil:
		return Acquisition{}, passiveErr
	case result.err != nil && ctx.Err() != nil:
		// The process was asked to stop while waiting. That is not a failure.
		return Acquisition{}, nil
	case result.err != nil:
		return Acquisition{}, result.err
	}
	return result.acquired, nil
}

// activate runs the Active composition and releases ownership once it returns.
//
// Release happens here rather than in the caller so that it cannot be forgotten
// on an error path, and so it always happens after Active has returned, which is
// the ordering the other instance depends on.
func activate(ctx context.Context, lock *Lock, runtime Runtime, kind ActivationKind) error {
	observer := operations.FromContext(ctx)
	started := time.Now()
	observer.Emit("platform.activation_started", operations.LevelInfo, "platform.redundancy", "active runtime activation started",
		map[string]any{operations.AttributeActivationKind: string(kind)})

	err := runtime.Active(ctx, kind)
	releaseErr := lock.Release()

	elapsed := time.Since(started).Milliseconds()
	if err != nil {
		observer.Emit("platform.activation_failed", operations.LevelError, "platform.redundancy", "active runtime activation failed",
			map[string]any{
				operations.AttributeActivationKind: string(kind),
				operations.AttributeDurationMS:     elapsed,
				operations.AttributeError:          err.Error(),
			})
		return errors.Join(err, releaseErr)
	}
	observer.Emit("platform.activation_completed", operations.LevelInfo, "platform.redundancy", "active runtime activation completed",
		map[string]any{operations.AttributeActivationKind: string(kind), operations.AttributeDurationMS: elapsed})
	return releaseErr
}

// emitAcquired reports ownership and, crucially, how it was obtained.
//
// An abandoned ownership means the previous owner died rather than handed over. The
// file-lock ownership this replaced could not tell the two apart, so an operator had
// to correlate logs to answer whether a failover was planned.
func emitAcquired(observer *operations.Recorder, lock *Lock, acquired Acquisition) {
	if lock == nil {
		return
	}
	attributes := map[string]any{
		operations.AttributeObject:    lock.Name(),
		operations.AttributeAbandoned: acquired.Abandoned,
	}
	if acquired.Abandoned {
		observer.Emit("platform.ownership_acquired", operations.LevelWarn, "platform.redundancy",
			"Primary Ownership acquired from a process that died without releasing it", attributes)
		return
	}
	observer.Emit("platform.ownership_acquired", operations.LevelInfo, "platform.redundancy", "Primary Ownership acquired", attributes)
}

// There is no branch here for "another process holds ownership on a machine with
// no standby". A machine that deploys no Standby Instance has no lock at all, so
// lock is nil, TryAcquire always succeeds, and this instance is Active by
// construction. A second copy of it fails when it cannot bind a port it was told
// to bind, which is stage 03's D3 ruling and is why the check the runtime used to
// make here was already unreachable.
