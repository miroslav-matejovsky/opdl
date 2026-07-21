package redundancy_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/redundancy"
)

// These cover the ownership lifecycle: which composition runs, in which order,
// and what happens on each way out. They are about sequencing rather than about
// the kernel object, which lock_test.go covers.

// recordingRuntime records the order the two compositions ran in, so a test can
// assert the property the whole state machine exists for: an instance's passive
// resources are closed before its active ones open.
type recordingRuntime struct {
	steps       atomic.Pointer[[]string]
	passiveErr  error
	activeErr   error
	activeKinds chan redundancy.ActivationKind
	// blockPassive keeps Passive running until its context is canceled, which is
	// what a real passive composition does.
	blockPassive bool
}

func newRecordingRuntime() *recordingRuntime {
	r := &recordingRuntime{activeKinds: make(chan redundancy.ActivationKind, 1), blockPassive: true}
	r.steps.Store(&[]string{})
	return r
}

func (r *recordingRuntime) record(step string) {
	for {
		current := r.steps.Load()
		next := append(append([]string{}, *current...), step)
		if r.steps.CompareAndSwap(current, &next) {
			return
		}
	}
}

func (r *recordingRuntime) recorded() []string { return *r.steps.Load() }

func (r *recordingRuntime) runtime() redundancy.Runtime {
	return redundancy.Runtime{
		Passive: func(ctx context.Context) error {
			r.record("passive start")
			if r.passiveErr != nil {
				r.record("passive failed")
				return r.passiveErr
			}
			if r.blockPassive {
				<-ctx.Done()
			}
			r.record("passive stop")
			return nil
		},
		Active: func(ctx context.Context, kind redundancy.ActivationKind) error {
			r.record("active start")
			r.activeKinds <- kind
			if r.activeErr != nil {
				r.record("active failed")
				return r.activeErr
			}
			<-ctx.Done()
			r.record("active stop")
			return nil
		},
	}
}

// TestContendActivatesImmediatelyWhenOwnershipIsFree checks the ordinary start:
// an instance that takes the lock uncontested never enters the passive state at
// all.
func TestContendActivatesImmediatelyWhenOwnershipIsFree(t *testing.T) {
	t.Parallel()

	lock := openLock(t, ownershipObject(t), redundancy.RolePrimary)
	runtime := newRecordingRuntime()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- redundancy.Contend(ctx, lock, runtime.runtime()) }()

	require.Equal(t, redundancy.ActivationInitial, <-runtime.activeKinds,
		"an uncontested start is an initial activation, not a failover")
	cancel()
	require.NoError(t, <-done)

	require.Equal(t, []string{"active start", "active stop"}, runtime.recorded(),
		"nothing passive runs when ownership was free")
}

// TestContendWithoutALockIsActiveByConstruction checks a machine that deploys no
// Standby Instance. There is no lock to contend for, so the lone Primary Instance
// is Active from the start and never waits for anything.
func TestContendWithoutALockIsActiveByConstruction(t *testing.T) {
	t.Parallel()

	runtime := newRecordingRuntime()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- redundancy.Contend(ctx, nil, runtime.runtime()) }()

	require.Equal(t, redundancy.ActivationInitial, <-runtime.activeKinds)
	cancel()
	require.NoError(t, <-done)
	require.Equal(t, []string{"active start", "active stop"}, runtime.recorded())
}

// TestContendRunsPassiveUntilOwnershipIsWon is the failover path, and it asserts
// the ordering the two compositions depend on: Passive has returned before Active
// starts.
//
// The two open the same journal storage and the same node identity, so an overlap
// is a machine running two of itself. Nothing in the type system prevents it; this
// is what does.
func TestContendRunsPassiveUntilOwnershipIsWon(t *testing.T) {
	t.Parallel()

	object := ownershipObject(t)
	holder := openLock(t, object, redundancy.RolePrimary)
	acquired, err := holder.TryAcquire()
	require.NoError(t, err)
	require.True(t, acquired.Held)

	standby := openLock(t, object, redundancy.RoleStandby)
	runtime := newRecordingRuntime()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- redundancy.Contend(ctx, standby, runtime.runtime()) }()

	// The standby is passive while the other instance holds ownership.
	require.Eventually(t, func() bool {
		return len(runtime.recorded()) == 1
	}, 5*time.Second, 10*time.Millisecond, "passive never started")
	require.Equal(t, []string{"passive start"}, runtime.recorded())

	require.NoError(t, holder.Release())

	require.Equal(t, redundancy.ActivationFailover, <-runtime.activeKinds,
		"a Standby Instance taking ownership is a failover")
	cancel()
	require.NoError(t, <-done)

	require.Equal(t, []string{"passive start", "passive stop", "active start", "active stop"}, runtime.recorded(),
		"the passive composition must be closed before the active one opens")
}

// TestContendCallsAPrimaryTakingOverAFailback checks the activation kind is
// derived from which instance won, not from how it won.
func TestContendCallsAPrimaryTakingOverAFailback(t *testing.T) {
	t.Parallel()

	object := ownershipObject(t)
	holder := openLock(t, object, redundancy.RoleStandby)
	acquired, err := holder.TryAcquire()
	require.NoError(t, err)
	require.True(t, acquired.Held)

	primary := openLock(t, object, redundancy.RolePrimary)
	runtime := newRecordingRuntime()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- redundancy.Contend(ctx, primary, runtime.runtime()) }()

	require.Eventually(t, func() bool { return len(runtime.recorded()) == 1 }, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, holder.Release())

	require.Equal(t, redundancy.ActivationFailback, <-runtime.activeKinds,
		"the Primary Instance taking ownership back is a failback")
	cancel()
	require.NoError(t, <-done)
}

// TestContendStopsWithoutActivatingWhenTheProcessIsStopped checks an instance
// asked to stop while waiting leaves quietly. Being told to stop is not a failure,
// and a stopping instance must not activate on its way out.
func TestContendStopsWithoutActivatingWhenTheProcessIsStopped(t *testing.T) {
	t.Parallel()

	object := ownershipObject(t)
	holder := openLock(t, object, redundancy.RolePrimary)
	acquired, err := holder.TryAcquire()
	require.NoError(t, err)
	require.True(t, acquired.Held)
	defer func() { require.NoError(t, holder.Release()) }()

	standby := openLock(t, object, redundancy.RoleStandby)
	runtime := newRecordingRuntime()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- redundancy.Contend(ctx, standby, runtime.runtime()) }()

	require.Eventually(t, func() bool { return len(runtime.recorded()) == 1 }, 5*time.Second, 10*time.Millisecond)
	cancel()

	require.NoError(t, <-done, "a process asked to stop while waiting has not failed")
	require.Equal(t, []string{"passive start", "passive stop"}, runtime.recorded(),
		"a stopping instance must not activate on its way out")
	require.False(t, standby.Held())
}

// TestContendReportsAPassiveCompositionThatGivesUp checks a passive composition
// that returns an error ends the instance rather than leaving it parked in a wait
// it can no longer honour.
//
// An instance that cannot follow the journal must not keep waiting for ownership
// it would be unable to use.
func TestContendReportsAPassiveCompositionThatGivesUp(t *testing.T) {
	t.Parallel()

	object := ownershipObject(t)
	holder := openLock(t, object, redundancy.RolePrimary)
	acquired, err := holder.TryAcquire()
	require.NoError(t, err)
	require.True(t, acquired.Held)
	defer func() { require.NoError(t, holder.Release()) }()

	standby := openLock(t, object, redundancy.RoleStandby)
	runtime := newRecordingRuntime()
	runtime.passiveErr = errors.New("projection unavailable")

	err = redundancy.Contend(t.Context(), standby, runtime.runtime())
	require.ErrorContains(t, err, "projection unavailable")
	require.Equal(t, []string{"passive start", "passive failed"}, runtime.recorded())
	require.False(t, standby.Held(), "an instance that gave up waiting holds nothing")
}

// TestContendReleasesOwnershipAfterTheActiveCompositionReturns is the ordering
// the other instance depends on.
//
// Releasing while this instance's active resources were still closing would let
// the other one open the same listeners and the same storage on top of them.
func TestContendReleasesOwnershipAfterTheActiveCompositionReturns(t *testing.T) {
	t.Parallel()

	lock := openLock(t, ownershipObject(t), redundancy.RolePrimary)
	runtime := newRecordingRuntime()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- redundancy.Contend(ctx, lock, runtime.runtime()) }()

	<-runtime.activeKinds
	require.True(t, lock.Held(), "ownership is held for the whole active composition")

	cancel()
	require.NoError(t, <-done)
	require.False(t, lock.Held(), "ownership is released once the active composition has returned")
}

// TestContendReleasesOwnershipWhenTheActiveCompositionFails checks the failure
// path releases too. An instance that failed to serve must not keep the other one
// out.
func TestContendReleasesOwnershipWhenTheActiveCompositionFails(t *testing.T) {
	t.Parallel()

	lock := openLock(t, ownershipObject(t), redundancy.RolePrimary)
	runtime := newRecordingRuntime()
	runtime.activeErr = errors.New("fabric would not open")

	err := redundancy.Contend(t.Context(), lock, runtime.runtime())
	require.ErrorContains(t, err, "fabric would not open")
	require.False(t, lock.Held(), "a failed activation still releases ownership")
}
