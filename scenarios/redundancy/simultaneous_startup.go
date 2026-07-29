package redundancy

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/scenarios/internal/harness"
)

// The machine the startup fixture deploys, and the one service its blueprint
// authors. Both instances probe that service, whichever of them owns the
// machine.
const (
	startupMachine = "node-a"
	startupService = "core-services"
)

// The two machine-scoped ownership facts this scenario reads out of the
// machine's own event store. They are spelled here rather than imported so the
// scenario reads the shipped record as a black box.
const (
	eventOwnershipAcquired = "platform.redundancy.ownership_acquired"
	eventFailbackInitiated = "platform.redundancy.failback_initiated"
)

// validAcquisitions are the two ownership sequences one start of a redundant
// machine may produce, once it has settled.
//
// Either instance may win the lease, so the sequence has two valid shapes and
// no more. The Primary winning outright is one acquisition; the Standby winning
// and handing the machine back under the preferred-primary policy is two. Any
// other sequence is ownership changing hands for a reason nothing here caused —
// a third acquisition, or a Standby that took the machine and kept it.
var validAcquisitions = [][]string{
	{harness.RolePrimary},
	{harness.RoleStandby, harness.RolePrimary},
}

// SimultaneousStartup is what a machine's two instances do when they are started
// together, which is how a machine that has just booted starts them.
//
// FailoverAndFailback starts the Primary first, so its exact event counts are
// deterministic. That is the right shape for the failover story and the wrong
// one for this: the condition worth testing here is precisely the race that
// ordering removes. Both instances are launched from one barrier, either may
// win the empty lease, and every assertion below is written to hold whichever
// does.
//
//  1. Both instances start at once and contest an empty lease. Exactly one wins.
//  2. Whoever won, the machine settles where the preferred-primary policy puts
//     it: the Primary serving, the Standby Passive and well.
//  3. Both instances watched the machine's service throughout. Probing is a
//     process's job, so which of them owns the machine is not supposed to change
//     who is watching what runs on it.
//  4. Both are killed and started together again, this time against a lease file
//     that already names a dead owner — a different branch of the same decision,
//     and the same properties.
//
// Throughout, a poller samples both instances and fails the run if both ever
// report Active.
func SimultaneousStartup(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	deployment := harness.DeploySite(ctx, t,
		filepath.Join(harness.ScenarioDir(t), "out"), filepath.Join(harness.ScenarioDir(t), "work"), "startup")
	node := deployment.Machine(t, startupMachine)
	machines := []*harness.Machine{node}

	manifest := harness.ReadManifest(t, node.BinaryPath)
	require.NotNil(t, manifest.Standby, "a machine that deploys a standby packages both launches")

	// The service both instances are authored to watch, bound before either of
	// them exists. What the scenario asks about it later is who probed it, not
	// whether it was there to probe.
	service := harness.StartService(t, node)

	primary, standby := node.StartTogether(ctx, t, manifest)
	diag := harness.DiagStringer(func() string {
		return fmt.Sprintf("--- primary process ---\n%s\n--- standby process ---\n%s\n--- %s ---\n%s",
			primary.Logs(), standby.Logs(), service, harness.Diagnose(machines))
	})

	// The split-brain poller runs for the whole scenario. It is checked
	// continuously rather than at the end because a machine that had two owners
	// for a second and one owner afterwards is a machine that failed, and a final
	// snapshot cannot tell it from one that never did.
	watch := watchForSplitBrain(ctx, node)

	// waitSettled blocks until the machine is where the preferred-primary policy
	// puts it, from either starting point: the Primary Active, the Standby Passive
	// and reporting nothing wrong with itself.
	waitSettled := func(what string) {
		t.Helper()
		harness.WaitFor(t, what, harness.APIWaitTimeout, pollInterval, func() bool {
			active, activeCode, activeErr := harness.FetchInstanceAt(ctx, node.URL)
			passive, passiveCode, passiveErr := harness.FetchInstanceAt(ctx, node.StandbyURL)
			if activeErr != nil || passiveErr != nil ||
				activeCode != http.StatusOK || passiveCode != http.StatusOK {
				return false
			}
			if active.State != harness.InstanceStateActive || passive.State != harness.InstanceStatePassive {
				return false
			}
			health, code, err := harness.FetchHealthAt(ctx, node.StandbyURL)
			return err == nil && code == http.StatusOK && health.Status == harness.HealthHealthy
		}, nil, diag)
	}

	waitSettled("the machine settling on its primary after a contested start")

	// The instance that lost the race is Passive, answering at its own address,
	// and well. From the ownership record alone a Standby that had quietly failed
	// would look exactly like one that simply lost, which is why its own account
	// of itself is asked for separately.
	loser, code, err := harness.FetchHealthAt(ctx, node.StandbyURL)
	require.NoError(t, err, "the instance that is not serving answers its health endpoint")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, harness.InstanceStatePassive, loser.RuntimeState)
	require.Equal(t, harness.HealthHealthy, loser.Status,
		"the instance that lost the race is well, not merely quiet")

	// The machine's own store says how ownership got where it is, and it is
	// allowed to say either of two things. Nothing below asks which.
	first := readOwnership(t, node)
	require.Containsf(t, validAcquisitions, first.acquisitions,
		"a contested start ends with the primary owning the machine, having taken it from nobody or from the standby and from nothing else: %v",
		first.acquisitions)
	if first.acquisitions[0] == harness.RoleStandby {
		require.Equalf(t, 1, first.failbacks,
			"the standby won the lease and gave the machine back, which is its own decision and is stated as one: %v",
			first.acquisitions)
	} else {
		require.Zerof(t, first.failbacks,
			"the primary won the lease outright, so there was nothing to hand back: %v", first.acquisitions)
	}

	// Both instances were watching the machine's service the whole time. The one
	// that lost the race is what proves it: monitoring is composed at process
	// lifetime rather than inside an activation, so a Passive instance probes and
	// publishes exactly as an Active one does.
	observers := harness.Observers(node)
	require.Len(t, observers, 2, "the machine deploys two instances and both serve the API")
	harness.WaitForServiceViews(ctx, t, "both instances reporting on the machine's service",
		observers, machines, func(view harness.ServiceView) bool {
			watched, ok := view.Unit(startupMachine, startupService)
			return ok && watched.Status == harness.ServiceHealthy &&
				watched.Fresh(harness.RolePrimary) && watched.Fresh(harness.RoleStandby)
		})
	require.Positivef(t, service.Requests(), "the service was really probed: %s", service)

	// -- and again, on a machine that has already run. -------------------------
	//
	// A machine that reboots does not start from an empty lease file. Killing
	// both instances leaves one that names an owner and was never released, so
	// the second race is contested against a grant that has to lapse first rather
	// than against nothing at all.

	require.NoError(t, primary.Kill())
	require.NoError(t, standby.Kill())

	primary, standby = node.StartTogether(ctx, t, manifest)
	waitSettled("the machine settling on its primary after a contested restart")

	// The machine's store is append-only, so the second start's account of itself
	// is what it added to the first's — and it is held to the same two shapes.
	second := readOwnership(t, node)
	require.GreaterOrEqual(t, len(second.acquisitions), len(first.acquisitions),
		"the machine's store is append-only: a restart adds to what it said, it does not revise it")
	require.Equal(t, first.acquisitions, second.acquisitions[:len(first.acquisitions)],
		"the machine's store is append-only: a restart adds to what it said, it does not revise it")
	restarted := second.acquisitions[len(first.acquisitions):]
	require.Containsf(t, validAcquisitions, restarted,
		"a restart with a lapsed lease in the file resolves to one owner the same way an empty one does: %v",
		second.acquisitions)

	harness.WaitForServiceViews(ctx, t, "both restarted instances reporting on the machine's service",
		observers, machines, func(view harness.ServiceView) bool {
			watched, ok := view.Unit(startupMachine, startupService)
			return ok && watched.Status == harness.ServiceHealthy &&
				watched.Fresh(harness.RolePrimary) && watched.Fresh(harness.RoleStandby)
		})

	require.Zero(t, watch.Observed(), "at no sampled instant did both instances report Active")

	// The two durable records agree, and neither of them depends on who won a
	// race. Each instance's state file counts what that instance did across its
	// processes; the machine's store says which instance did it. The primary was
	// launched twice and served the machine once per start, whether it took
	// ownership at once or was handed it; the standby served exactly as often as
	// the machine says it took ownership, which is nought, once, or twice.
	primaryEpochs := harness.InstanceEpoch(t, node.Sockets.StateFile)
	require.Equal(t, harness.InstanceEpochs{Epoch: 4, Process: 2, Activation: 2}, primaryEpochs,
		"the primary started twice and ended up serving after each start")

	standbyWins := second.wins(harness.RoleStandby)
	standbyEpochs := harness.InstanceEpoch(t, node.Sockets.StandbyStateFile)
	require.Equal(t, harness.InstanceEpochs{Epoch: 2 + standbyWins, Process: 2, Activation: standbyWins}, standbyEpochs,
		"the standby started twice and was Active exactly as often as the machine's store says it took ownership")
}

// readOwnership returns what a machine's own event store says happened to
// Primary Ownership, in the order it happened.
//
// The store is the machine's rather than an instance's, so one file holds both
// instances' side of a handover. That is what makes an ownership sequence
// readable at all without correlating two records written by two processes that
// were racing each other.
func readOwnership(t *testing.T, m *harness.Machine) ownership {
	t.Helper()
	var seen ownership
	for _, event := range harness.MachineEvents(t, m) {
		switch event.Type {
		case eventOwnershipAcquired:
			seen.acquisitions = append(seen.acquisitions, event.Origin.ProcessRole)
		case eventFailbackInitiated:
			seen.failbacks++
		}
	}
	require.NotEmpty(t, seen.acquisitions, "%s has been serving, so something took ownership of it", m.Name)
	return seen
}

// ownership is a machine's ownership history as its own store tells it.
type ownership struct {
	// acquisitions is which instance took Primary Ownership, in order.
	acquisitions []string
	// failbacks is how many times an Active Standby began giving the machine back
	// to the Primary. It is what distinguishes a Standby that won the lease from
	// one that never held it, after the machine has settled and both look alike.
	failbacks int
}

// wins is how many times one instance took Primary Ownership.
func (o ownership) wins(role string) uint64 {
	var won uint64
	for _, acquired := range o.acquisitions {
		if acquired == role {
			won++
		}
	}
	return won
}

// splitBrainWatch samples both of a machine's instances until it is stopped and
// counts the instants at which both reported Active.
type splitBrainWatch struct {
	stop     context.CancelFunc
	done     chan struct{}
	observed atomic.Int32
}

// watchForSplitBrain starts sampling both of a machine's instances. It reports
// rather than asserts, because it runs on its own goroutine: a failed assertion
// there would stop the poller instead of the scenario.
func watchForSplitBrain(ctx context.Context, m *harness.Machine) *splitBrainWatch {
	ctx, cancel := context.WithCancel(ctx)
	w := &splitBrainWatch{stop: cancel, done: make(chan struct{})}
	go func() {
		defer close(w.done)
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p, pCode, pErr := harness.FetchInstanceAt(ctx, m.URL)
				s, sCode, sErr := harness.FetchInstanceAt(ctx, m.StandbyURL)
				if pErr == nil && sErr == nil && pCode == http.StatusOK && sCode == http.StatusOK &&
					p.State == harness.InstanceStateActive && s.State == harness.InstanceStateActive {
					w.observed.Add(1)
				}
			}
		}
	}()
	return w
}

// Observed stops the watch and returns how many sampled instants found the
// machine with two owners.
func (w *splitBrainWatch) Observed() int32 {
	w.stop()
	<-w.done
	return w.observed.Load()
}
