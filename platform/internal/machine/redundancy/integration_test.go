package redundancy_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/machine/redundancy"
)

// These are the redundancy package's integration tests: two real ManageOwnership
// calls over one shared lease file, which is exactly the shape a machine runs.
// Where ownership_test.go proves single behaviors with a scripted peer, these
// prove whole lifecycles with the peer's health derived from whether the peer is
// actually running — the way the real app wires the health gate.
//
// A split-brain sampler watches both instances throughout the takeover tests and
// fails the run if both ever report Active in the same sample. In-process
// sampling cannot catch every interleaving; it pins the invariant against
// regressions.

// testMachine is one machine under test: the shared lease policy, and one
// health and one activity flag per instance. The health flags are what each
// instance's promotion gate reads about its peer; the activity flags are what
// the split-brain sampler reads.
type testMachine struct {
	t   *testing.T
	cfg redundancy.LeaseConfig

	up     map[redundancy.InstanceRole]*atomic.Bool
	active map[redundancy.InstanceRole]*atomic.Bool
}

// newTestMachine returns a machine whose lease lives on a fresh temp file.
// stabilization is the failback window; the flap test widens it, the handover
// tests keep it short.
func newTestMachine(t *testing.T, stabilization time.Duration) *testMachine {
	t.Helper()
	cfg := leaseConfig(t)
	cfg.FailbackStabilization = stabilization
	return &testMachine{
		t:   t,
		cfg: cfg,
		up: map[redundancy.InstanceRole]*atomic.Bool{
			redundancy.RolePrimary: {}, redundancy.RoleStandby: {},
		},
		active: map[redundancy.InstanceRole]*atomic.Bool{
			redundancy.RolePrimary: {}, redundancy.RoleStandby: {},
		},
	}
}

func otherRole(role redundancy.InstanceRole) redundancy.InstanceRole {
	if role == redundancy.RolePrimary {
		return redundancy.RoleStandby
	}
	return redundancy.RolePrimary
}

// deps is the health gate one instance runs with: its peer is healthy exactly
// while the peer instance is running (or scripted to look so).
func (m *testMachine) deps(role redundancy.InstanceRole) redundancy.Deps {
	peer := m.up[otherRole(role)]
	return redundancy.Deps{PeerHealthy: func(context.Context) bool { return peer.Load() }}
}

// crashedPrimaryHoldsTheLease writes the aftermath of a crashed Active Primary:
// a valid grant in the lease file's documented shape, with nobody left to renew
// it or answer health checks. The grant lapses one lease duration later.
func (m *testMachine) crashedPrimaryHoldsTheLease() {
	m.t.Helper()
	grant := fmt.Sprintf(`{"owner_role":"primary","owner_pid":4242,"expires_unix_nano":%d}`,
		time.Now().Add(m.cfg.Duration).UnixNano())
	require.NoError(m.t, os.WriteFile(m.cfg.File, []byte(grant), 0o644))
}

// watchForSplitBrain samples both instances' activity until the returned assert
// runs, and fails the test if both were ever Active in the same sample.
func (m *testMachine) watchForSplitBrain() (assertClean func()) {
	ctx, cancel := context.WithCancel(context.Background())
	var violated atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if m.active[redundancy.RolePrimary].Load() && m.active[redundancy.RoleStandby].Load() {
					violated.Store(true)
				}
			}
		}
	}()
	m.t.Cleanup(func() { cancel(); <-done })
	return func() {
		cancel()
		<-done
		require.False(m.t, violated.Load(), "both instances reported Active at the same instant")
	}
}

// testInstance is one running instance: a real ManageOwnership call, the runtime
// recording its turns, and the events it stated.
type testInstance struct {
	t       *testing.T
	role    redundancy.InstanceRole
	machine *testMachine
	runtime *recordingRuntime
	events  func() []events.Envelope
	cancel  context.CancelFunc
	done    chan error

	haltOnce sync.Once
	haltErr  error
}

// start brings one instance up: it opens its own lease handle on the shared
// file, begins answering health (up), and runs ManageOwnership until stopped.
func (m *testMachine) start(role redundancy.InstanceRole) *testInstance {
	m.t.Helper()
	lease := openLease(m.t, m.cfg, role)
	rt := newRecordingRuntime()
	publisher, recorded := recording(m.t)

	run := rt.runtime()
	innerActive := run.Active
	activeFlag := m.active[role]
	run.Active = func(ctx context.Context, kind redundancy.ActivationKind) error {
		activeFlag.Store(true)
		defer activeFlag.Store(false)
		return innerActive(ctx, kind)
	}

	ctx, cancel := context.WithCancel(m.t.Context())
	inst := &testInstance{
		t: m.t, role: role, machine: m,
		runtime: rt, events: recorded,
		cancel: cancel, done: make(chan error, 1),
	}
	m.up[role].Store(true)
	go func() { inst.done <- redundancy.ManageOwnership(ctx, publisher, lease, m.deps(role), run) }()
	// The goroutine is drained whatever happens to the test, so t.TempDir cleanup
	// never races a lease write.
	m.t.Cleanup(func() { _ = inst.halt() })
	return inst
}

// halt stops the instance the way a process stop does — cancel, wait, and stop
// answering health — and is safe to call twice.
func (i *testInstance) halt() error {
	i.haltOnce.Do(func() {
		i.cancel()
		i.haltErr = <-i.done
		i.machine.up[i.role].Store(false)
	})
	return i.haltErr
}

// stop is a graceful stop that must succeed.
func (i *testInstance) stop() {
	i.t.Helper()
	require.NoError(i.t, i.halt())
}

// stillManaging reports whether the ManageOwnership call is still running, which
// is what "in place" means: no transition may end it.
func (i *testInstance) stillManaging() bool {
	select {
	case err := <-i.done:
		i.done <- err
		return false
	default:
		return true
	}
}

// awaitActivation waits for the instance's next Active turn and returns why it
// became Active.
func awaitActivation(t *testing.T, inst *testInstance) redundancy.ActivationKind {
	t.Helper()
	select {
	case kind := <-inst.runtime.activeKinds:
		return kind
	case <-time.After(5 * time.Second):
		t.Fatalf("%s never became Active", inst.role)
		return ""
	}
}

// acquisitionsOf decodes every ownership acquisition an instance stated.
func acquisitionsOf(t *testing.T, inst *testInstance) []redundancy.OwnershipAcquired {
	t.Helper()
	var out []redundancy.OwnershipAcquired
	for _, envelope := range inst.events() {
		if envelope.Type != redundancy.TypeOwnershipAcquired {
			continue
		}
		var payload redundancy.OwnershipAcquired
		require.NoError(t, json.Unmarshal(envelope.Data, &payload))
		out = append(out, payload)
	}
	return out
}

// hasEvent reports whether the instance stated at least one event of the kind.
func hasEvent(inst *testInstance, kind events.Type) bool {
	for _, envelope := range inst.events() {
		if envelope.Type == kind {
			return true
		}
	}
	return false
}

// TestMachineRunsPrimaryActiveAndStandbyPassive is the machine's normal life:
// the Primary starts first and serves, the Standby joins and waits, and both
// stop cleanly with no failover ever stated.
func TestMachineRunsPrimaryActiveAndStandbyPassive(t *testing.T) {
	t.Parallel()

	m := newTestMachine(t, 300*time.Millisecond)
	primary := m.start(redundancy.RolePrimary)
	require.Equal(t, redundancy.ActivationInitial, awaitActivation(t, primary))

	standby := m.start(redundancy.RoleStandby)
	require.Eventually(t, func() bool {
		return len(standby.runtime.recorded()) == 1
	}, 5*time.Second, 10*time.Millisecond, "the standby never entered the passive state")

	// The standby stops first — a machine shutting down stops its standby before
	// its active so the stop is not mistaken for a failover.
	standby.stop()
	primary.stop()

	require.Equal(t, []string{"passive start", "passive stop"}, standby.runtime.recorded(),
		"a standby beside a healthy primary never activates")
	require.Empty(t, acquisitionsOf(t, standby), "nothing was acquired, so nothing may say it was")
	require.Equal(t, []events.Type{
		redundancy.TypeLeaseOpened,
		redundancy.TypeOwnershipAcquired,
		redundancy.TypeActivationStarted,
		redundancy.TypeActivationCompleted,
	}, types(primary.events()), "the primary's record is one clean turn")
}

// TestMachineFailsOverWhenThePrimaryStopsRenewing is the crash failover: the
// Primary died holding a valid grant, so the Standby waits out the lapse and
// takes over, recording that the grant was abandoned rather than handed over.
func TestMachineFailsOverWhenThePrimaryStopsRenewing(t *testing.T) {
	t.Parallel()

	m := newTestMachine(t, 300*time.Millisecond)
	m.crashedPrimaryHoldsTheLease()
	assertClean := m.watchForSplitBrain()

	standby := m.start(redundancy.RoleStandby)
	require.Equal(t, redundancy.ActivationFailover, awaitActivation(t, standby),
		"taking over from a dead primary is a failover")

	acquisitions := acquisitionsOf(t, standby)
	require.Len(t, acquisitions, 1)
	require.True(t, acquisitions[0].Abandoned,
		"a grant that lapsed without a release was abandoned, and the record says so")

	standby.stop()
	assertClean()
}

// TestMachineDoesNotFailOverWhileThePrimaryStillServes is the health gate over a
// real lapse: the recorded owner stopped renewing but still answers its health
// endpoint, so the Standby declines — a slow Primary is not failed over. Once
// the Primary actually goes away, the same Standby promotes.
func TestMachineDoesNotFailOverWhileThePrimaryStillServes(t *testing.T) {
	t.Parallel()

	m := newTestMachine(t, time.Hour)
	m.crashedPrimaryHoldsTheLease()
	m.up[redundancy.RolePrimary].Store(true) // stopped renewing, still answering

	standby := m.start(redundancy.RoleStandby)

	require.Eventually(t, func() bool {
		return hasEvent(standby, redundancy.TypePromotionDeclined)
	}, 5*time.Second, 10*time.Millisecond, "the declined promotion is stated for failover troubleshooting")
	require.Equal(t, []string{"passive start"}, standby.runtime.recorded(),
		"a lapsed lease alone does not promote past a serving peer")

	m.up[redundancy.RolePrimary].Store(false)
	require.Equal(t, redundancy.ActivationFailover, awaitActivation(t, standby),
		"the moment the peer is gone, the lapsed lease is taken")

	standby.stop()
}

// TestMachineFailsBackOnceThePrimaryReturns is the Preferred Primary policy end
// to end: the Standby serves while the Primary is away, and once the returned
// Primary has been healthy for the stabilization window the Standby steps down
// in place — still running, back to Passive — and the Primary takes over.
func TestMachineFailsBackOnceThePrimaryReturns(t *testing.T) {
	t.Parallel()

	m := newTestMachine(t, 100*time.Millisecond)
	m.crashedPrimaryHoldsTheLease()
	assertClean := m.watchForSplitBrain()

	standby := m.start(redundancy.RoleStandby)
	require.Equal(t, redundancy.ActivationFailover, awaitActivation(t, standby))

	primary := m.start(redundancy.RolePrimary)
	require.Equal(t, redundancy.ActivationFailback, awaitActivation(t, primary),
		"the returning Primary reclaims ownership once the Standby hands it back")

	// The standby's whole story: it waited, took over, handed back, and waits
	// again — five steps, all inside one call.
	require.Eventually(t, func() bool {
		steps := standby.runtime.recorded()
		return len(steps) == 5 && steps[4] == "passive start"
	}, 5*time.Second, 10*time.Millisecond, "the standby re-enters the passive state after handing over")

	require.True(t, standby.stillManaging(), "a failback must not end the standby")
	require.True(t, hasEvent(standby, redundancy.TypeFailbackInitiated))
	for _, acquisition := range acquisitionsOf(t, primary) {
		require.False(t, acquisition.Abandoned, "a failback is a handover, not a takeover from the dead")
	}

	assertClean()
	standby.stop()
	primary.stop()
}

// TestMachineDoesNotFailBackToAFlappingPrimary is the stabilization window
// doing its job: a Primary that keeps dropping in and out never stays healthy
// long enough, so the Standby keeps serving. Once the Primary is steadily
// healthy, the handover happens.
func TestMachineDoesNotFailBackToAFlappingPrimary(t *testing.T) {
	t.Parallel()

	m := newTestMachine(t, 400*time.Millisecond)
	standby := m.start(redundancy.RoleStandby)
	require.Equal(t, redundancy.ActivationInitial, awaitActivation(t, standby))

	// The Primary flaps: never continuously healthy for anything near the
	// stabilization window, so every healthy stretch is reset.
	primaryUp := m.up[redundancy.RolePrimary]
	for range 6 {
		primaryUp.Store(true)
		time.Sleep(70 * time.Millisecond)
		primaryUp.Store(false)
		time.Sleep(70 * time.Millisecond)
	}
	require.Equal(t, []string{"active start"}, standby.runtime.recorded(),
		"a flapping primary never earns the handover")

	// Steady health earns it.
	primaryUp.Store(true)
	require.Eventually(t, func() bool {
		steps := standby.runtime.recorded()
		return len(steps) == 3 && steps[2] == "passive start"
	}, 5*time.Second, 10*time.Millisecond, "a steadily healthy primary gets ownership back")

	standby.stop()
}

// TestMachineAlternatesOwnershipAcrossPrimaryLifetimes drives one Standby call
// through the whole story twice: the Primary serves and stops, the Standby takes
// over, the Primary returns and gets ownership back, then stops again and the
// Standby takes over again — with every transition in place and nobody exiting
// but the Primary processes themselves.
func TestMachineAlternatesOwnershipAcrossPrimaryLifetimes(t *testing.T) {
	t.Parallel()

	m := newTestMachine(t, 60*time.Millisecond)
	assertClean := m.watchForSplitBrain()

	firstPrimary := m.start(redundancy.RolePrimary)
	require.Equal(t, redundancy.ActivationInitial, awaitActivation(t, firstPrimary))

	standby := m.start(redundancy.RoleStandby)
	require.Eventually(t, func() bool {
		return len(standby.runtime.recorded()) == 1
	}, 5*time.Second, 10*time.Millisecond)

	// The Primary stops gracefully; its released grant is an immediate handover.
	firstPrimary.stop()
	require.Equal(t, redundancy.ActivationFailover, awaitActivation(t, standby))
	acquisitions := acquisitionsOf(t, standby)
	require.Len(t, acquisitions, 1)
	require.False(t, acquisitions[0].Abandoned, "a graceful stop hands over; nothing was abandoned")

	// The Primary returns, waits out the stabilization window, and reclaims.
	secondPrimary := m.start(redundancy.RolePrimary)
	require.Equal(t, redundancy.ActivationFailback, awaitActivation(t, secondPrimary))
	require.Eventually(t, func() bool {
		steps := standby.runtime.recorded()
		return len(steps) == 5 && steps[4] == "passive start"
	}, 5*time.Second, 10*time.Millisecond, "the standby is passive again after the failback")

	// The Primary stops again; the Standby takes over again, all in one call.
	secondPrimary.stop()
	require.Equal(t, redundancy.ActivationFailover, awaitActivation(t, standby))

	require.True(t, standby.stillManaging(), "two whole cycles and the standby never exited")
	require.Equal(t, []string{
		"passive start", "passive stop", "active start", "active stop",
		"passive start", "passive stop", "active start",
	}, standby.runtime.recorded(), "the turns alternate strictly, in place")

	assertClean()
	standby.stop()
}
