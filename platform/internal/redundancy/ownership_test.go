package redundancy_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/redundancy"
)

// These cover the ownership lifecycle: which composition runs, in which order,
// and what happens on each way out. They are about sequencing rather than about
// the lease record, which lease_internal_test.go covers.

// leaseConfig returns a lease policy on a fresh temp file with timings short
// enough to make a failover happen in milliseconds. Two instances that contend
// must be opened from the same config so they share one file.
func leaseConfig(t *testing.T) redundancy.LeaseConfig {
	t.Helper()
	return redundancy.LeaseConfig{
		File:                filepath.Join(t.TempDir(), "lease"),
		Duration:            200 * time.Millisecond,
		RenewalInterval:     40 * time.Millisecond,
		HealthCheckInterval: 15 * time.Millisecond,
	}
}

func openLease(t *testing.T, cfg redundancy.LeaseConfig, role redundancy.InstanceRole) *redundancy.Lease {
	t.Helper()
	lease, err := redundancy.OpenLease(cfg, role)
	require.NoError(t, err)
	return lease
}

// unhealthyPeer is the health gate a Passive instance is given when the test wants
// promotion to be allowed the moment the lease is free: the peer is reported gone.
var unhealthyPeer = redundancy.Deps{PeerHealthy: func(context.Context) bool { return false }}

// healthyPeer reports the peer as always able to serve, so a Passive instance
// never promotes however free the lease looks.
var healthyPeer = redundancy.Deps{PeerHealthy: func(context.Context) bool { return true }}

// stating is the process-local publisher these tests hand Contend. They assert on
// sequencing rather than on what was stated, so the envelopes go to a backend
// nobody reads. What was stated is events_test.go's subject.
func stating(t *testing.T) events.Publisher {
	t.Helper()
	publisher, _ := recording(t)
	return publisher
}

// recordingRuntime records the order the two compositions ran in, so a test can
// assert the property the whole state machine exists for: an instance's passive
// resources are closed before its active ones open.
type recordingRuntime struct {
	steps        atomic.Pointer[[]string]
	passiveErr   error
	activeErr    error
	activeKinds  chan redundancy.ActivationKind
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
// an instance that finds the lease free never enters the passive state at all.
func TestContendActivatesImmediatelyWhenOwnershipIsFree(t *testing.T) {
	t.Parallel()

	lease := openLease(t, leaseConfig(t), redundancy.RolePrimary)
	runtime := newRecordingRuntime()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- redundancy.Contend(ctx, stating(t), lease, unhealthyPeer, runtime.runtime()) }()

	require.Equal(t, redundancy.ActivationInitial, <-runtime.activeKinds,
		"an uncontested start is an initial activation, not a failover")
	cancel()
	require.NoError(t, <-done)

	require.Equal(t, []string{"active start", "active stop"}, runtime.recorded(),
		"nothing passive runs when the lease was free")
}

// TestContendWithoutALeaseIsActiveByConstruction checks a machine that deploys no
// Standby Instance. There is no lease to contend for, so the lone Primary Instance
// is Active from the start and never waits for anything.
func TestContendWithoutALeaseIsActiveByConstruction(t *testing.T) {
	t.Parallel()

	runtime := newRecordingRuntime()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- redundancy.Contend(ctx, stating(t), nil, redundancy.Deps{}, runtime.runtime()) }()

	require.Equal(t, redundancy.ActivationInitial, <-runtime.activeKinds)
	cancel()
	require.NoError(t, <-done)
	require.Equal(t, []string{"active start", "active stop"}, runtime.recorded())
}

// TestContendFailsOverWhenTheHolderReleases is the failover path, and it asserts
// the ordering the two compositions depend on: Passive has returned before Active
// starts. Two real Contend calls share one lease file, exactly as a machine's two
// instances do.
func TestContendFailsOverWhenTheHolderReleases(t *testing.T) {
	t.Parallel()

	cfg := leaseConfig(t)
	holder := openLease(t, cfg, redundancy.RolePrimary)
	standby := openLease(t, cfg, redundancy.RoleStandby)

	holderRuntime := newRecordingRuntime()
	holderCtx, stopHolder := context.WithCancel(t.Context())
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- redundancy.Contend(holderCtx, stating(t), holder, redundancy.Deps{}, holderRuntime.runtime())
	}()
	require.Equal(t, redundancy.ActivationInitial, <-holderRuntime.activeKinds, "the holder takes the free lease")

	standbyRuntime := newRecordingRuntime()
	standbyCtx, stopStandby := context.WithCancel(t.Context())
	standbyDone := make(chan error, 1)
	go func() {
		standbyDone <- redundancy.Contend(standbyCtx, stating(t), standby, unhealthyPeer, standbyRuntime.runtime())
	}()

	// The standby is passive while the holder's lease is valid.
	require.Eventually(t, func() bool {
		return len(standbyRuntime.recorded()) == 1
	}, 5*time.Second, 10*time.Millisecond, "passive never started")
	require.Equal(t, []string{"passive start"}, standbyRuntime.recorded())

	// The holder releases by shutting down; the standby takes over.
	stopHolder()
	require.NoError(t, <-holderDone)

	require.Equal(t, redundancy.ActivationFailover, <-standbyRuntime.activeKinds,
		"a Standby Instance taking ownership is a failover")

	require.Equal(t, []string{"passive start", "passive stop", "active start"}, standbyRuntime.recorded(),
		"the passive composition must be closed before the active one opens")

	stopStandby()
	<-standbyDone
}

// TestContendDoesNotPromoteWhileThePeerIsHealthy checks the health gate: a free
// lease alone does not promote a Passive instance. The holder here never releases,
// so the standby stays passive; a healthy-peer gate would keep it passive even if
// the lease had lapsed.
func TestContendDoesNotPromoteWhileThePeerIsHealthy(t *testing.T) {
	t.Parallel()

	cfg := leaseConfig(t)
	holder := openLease(t, cfg, redundancy.RolePrimary)
	standby := openLease(t, cfg, redundancy.RoleStandby)

	holderRuntime := newRecordingRuntime()
	holderCtx, stopHolder := context.WithCancel(t.Context())
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- redundancy.Contend(holderCtx, stating(t), holder, redundancy.Deps{}, holderRuntime.runtime())
	}()
	require.Equal(t, redundancy.ActivationInitial, <-holderRuntime.activeKinds)

	standbyRuntime := newRecordingRuntime()
	standbyCtx, stopStandby := context.WithCancel(t.Context())
	standbyDone := make(chan error, 1)
	go func() {
		standbyDone <- redundancy.Contend(standbyCtx, stating(t), standby, healthyPeer, standbyRuntime.runtime())
	}()

	require.Eventually(t, func() bool { return len(standbyRuntime.recorded()) == 1 }, 5*time.Second, 10*time.Millisecond)

	// Give the health-check loop several ticks to (not) promote.
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, []string{"passive start"}, standbyRuntime.recorded(),
		"a Passive instance does not promote while its peer holds a valid lease")

	stopStandby()
	<-standbyDone
	stopHolder()
	<-holderDone
}

// TestContendStandbyFailsBackToAHealthyPrimary checks the automatic failback: an
// Active Standby whose peer (the Primary) has been healthy for the stabilization
// window steps down on its own, so the preferred Primary can reclaim ownership.
// Contend returns without the context being canceled, which is the step-down the
// service manager restarts the Standby Passive after.
func TestContendStandbyFailsBackToAHealthyPrimary(t *testing.T) {
	t.Parallel()

	cfg := leaseConfig(t)
	cfg.FailbackStabilization = 60 * time.Millisecond
	standby := openLease(t, cfg, redundancy.RoleStandby)

	runtime := newRecordingRuntime()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- redundancy.Contend(ctx, stating(t), standby, healthyPeer, runtime.runtime()) }()

	require.Equal(t, redundancy.ActivationInitial, <-runtime.activeKinds, "the standby takes the free lease")

	// The Primary is healthy, so once the stabilization window passes the standby
	// hands ownership back by stepping down of its own accord.
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the standby did not fail back to the healthy primary")
	}
	require.Equal(t, []string{"active start", "active stop"}, runtime.recorded())
	require.False(t, standby.Held(), "the standby released the lease on failback")
}

// TestContendDoesNotFailBackToAnUnhealthyPeer checks the stabilization gate: an
// Active Standby whose peer is not answering does not hand ownership back, because
// there is no healthy Primary to hand it to.
func TestContendDoesNotFailBackToAnUnhealthyPeer(t *testing.T) {
	t.Parallel()

	cfg := leaseConfig(t)
	cfg.FailbackStabilization = 60 * time.Millisecond
	standby := openLease(t, cfg, redundancy.RoleStandby)

	runtime := newRecordingRuntime()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- redundancy.Contend(ctx, stating(t), standby, unhealthyPeer, runtime.runtime()) }()

	require.Equal(t, redundancy.ActivationInitial, <-runtime.activeKinds)

	// Several stabilization windows pass with no healthy peer; the standby stays
	// Active rather than stepping down into an empty handover.
	select {
	case err := <-done:
		t.Fatalf("the standby failed back with no healthy primary to take over: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	require.True(t, standby.Held(), "the standby keeps ownership while its peer is down")

	cancel()
	<-done
}

// TestContendCallsAPrimaryTakingOverAFailback checks the activation kind is
// derived from which instance won, not from how it won.
func TestContendCallsAPrimaryTakingOverAFailback(t *testing.T) {
	t.Parallel()

	cfg := leaseConfig(t)
	holder := openLease(t, cfg, redundancy.RoleStandby)
	primary := openLease(t, cfg, redundancy.RolePrimary)

	holderRuntime := newRecordingRuntime()
	holderCtx, stopHolder := context.WithCancel(t.Context())
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- redundancy.Contend(holderCtx, stating(t), holder, redundancy.Deps{}, holderRuntime.runtime())
	}()
	require.Equal(t, redundancy.ActivationInitial, <-holderRuntime.activeKinds)

	primaryRuntime := newRecordingRuntime()
	primaryCtx, stopPrimary := context.WithCancel(t.Context())
	primaryDone := make(chan error, 1)
	go func() {
		primaryDone <- redundancy.Contend(primaryCtx, stating(t), primary, unhealthyPeer, primaryRuntime.runtime())
	}()
	require.Eventually(t, func() bool { return len(primaryRuntime.recorded()) == 1 }, 5*time.Second, 10*time.Millisecond)

	stopHolder()
	require.NoError(t, <-holderDone)
	require.Equal(t, redundancy.ActivationFailback, <-primaryRuntime.activeKinds,
		"the Primary Instance taking ownership back is a failback")

	stopPrimary()
	<-primaryDone
}

// TestContendStopsWithoutActivatingWhenTheProcessIsStopped checks an instance
// asked to stop while waiting leaves quietly. Being told to stop is not a failure,
// and a stopping instance must not activate on its way out.
func TestContendStopsWithoutActivatingWhenTheProcessIsStopped(t *testing.T) {
	t.Parallel()

	cfg := leaseConfig(t)
	holder := openLease(t, cfg, redundancy.RolePrimary)
	holderCtx, stopHolder := context.WithCancel(t.Context())
	holderDone := make(chan error, 1)
	holderRuntime := newRecordingRuntime()
	go func() {
		holderDone <- redundancy.Contend(holderCtx, stating(t), holder, redundancy.Deps{}, holderRuntime.runtime())
	}()
	require.Equal(t, redundancy.ActivationInitial, <-holderRuntime.activeKinds)

	standby := openLease(t, cfg, redundancy.RoleStandby)
	standbyRuntime := newRecordingRuntime()
	standbyCtx, stopStandby := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- redundancy.Contend(standbyCtx, stating(t), standby, unhealthyPeer, standbyRuntime.runtime())
	}()

	require.Eventually(t, func() bool { return len(standbyRuntime.recorded()) == 1 }, 5*time.Second, 10*time.Millisecond)
	stopStandby()

	require.NoError(t, <-done, "a process asked to stop while waiting has not failed")
	require.Equal(t, []string{"passive start", "passive stop"}, standbyRuntime.recorded(),
		"a stopping instance must not activate on its way out")
	require.False(t, standby.Held())

	stopHolder()
	<-holderDone
}

// TestContendReportsAPassiveCompositionThatGivesUp checks a passive composition
// that returns an error ends the instance rather than leaving it parked in a wait
// it can no longer honour.
func TestContendReportsAPassiveCompositionThatGivesUp(t *testing.T) {
	t.Parallel()

	cfg := leaseConfig(t)
	holder := openLease(t, cfg, redundancy.RolePrimary)
	holderCtx, stopHolder := context.WithCancel(t.Context())
	holderDone := make(chan error, 1)
	holderRuntime := newRecordingRuntime()
	go func() {
		holderDone <- redundancy.Contend(holderCtx, stating(t), holder, redundancy.Deps{}, holderRuntime.runtime())
	}()
	require.Equal(t, redundancy.ActivationInitial, <-holderRuntime.activeKinds)

	standby := openLease(t, cfg, redundancy.RoleStandby)
	standbyRuntime := newRecordingRuntime()
	standbyRuntime.passiveErr = errors.New("projection unavailable")

	err := redundancy.Contend(t.Context(), stating(t), standby, unhealthyPeer, standbyRuntime.runtime())
	require.ErrorContains(t, err, "projection unavailable")
	require.Equal(t, []string{"passive start", "passive failed"}, standbyRuntime.recorded())
	require.False(t, standby.Held(), "an instance that gave up waiting holds nothing")

	stopHolder()
	<-holderDone
}

// TestContendReleasesOwnershipAfterTheActiveCompositionReturns is the ordering
// the other instance depends on.
func TestContendReleasesOwnershipAfterTheActiveCompositionReturns(t *testing.T) {
	t.Parallel()

	lease := openLease(t, leaseConfig(t), redundancy.RolePrimary)
	runtime := newRecordingRuntime()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- redundancy.Contend(ctx, stating(t), lease, unhealthyPeer, runtime.runtime()) }()

	<-runtime.activeKinds
	require.True(t, lease.Held(), "ownership is held for the whole active composition")

	cancel()
	require.NoError(t, <-done)
	require.False(t, lease.Held(), "ownership is released once the active composition has returned")
}

// TestContendReleasesOwnershipWhenTheActiveCompositionFails checks the failure
// path releases too. An instance that failed to serve must not keep the other one
// out.
func TestContendReleasesOwnershipWhenTheActiveCompositionFails(t *testing.T) {
	t.Parallel()

	lease := openLease(t, leaseConfig(t), redundancy.RolePrimary)
	runtime := newRecordingRuntime()
	runtime.activeErr = errors.New("fabric would not open")

	err := redundancy.Contend(t.Context(), stating(t), lease, unhealthyPeer, runtime.runtime())
	require.ErrorContains(t, err, "fabric would not open")
	require.False(t, lease.Held(), "a failed activation still releases ownership")
}
