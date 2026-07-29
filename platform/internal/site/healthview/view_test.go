package healthview_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/site/healthview"
)

// The view is pure, so none of this needs a broker or a wall clock. Freshness
// is exercised by moving an injected clock, which is what makes expiry a step a
// test takes rather than one it waits for.

const freshFor = 22 * time.Second

var deployment = healthview.Deployment{Project: "customer-a", Environment: "production", Site: "north"}

// testClock is the receiver's clock under the test's control.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// redundantUnit is a service on a machine that deploys both instances, so two
// observers are expected to report on it.
func redundantUnit(machine, service string) healthview.Unit {
	return healthview.Unit{
		UnitKey:        healthview.UnitKey{Machine: machine, Service: service},
		MachineProfile: "sensor-node",
		ServiceRole:    "master",
		ObserverRoles:  []string{"primary", "standby"},
		FreshFor:       freshFor,
	}
}

// soloUnit is a service on a machine with no standby: one expected observer.
func soloUnit(machine, service string) healthview.Unit {
	unit := redundantUnit(machine, service)
	unit.ObserverRoles = []string{"primary"}
	return unit
}

func newView(t *testing.T, clock healthview.Clock, inventory ...healthview.Unit) *healthview.View {
	t.Helper()
	view, err := healthview.New(deployment, inventory, clock)
	require.NoError(t, err)
	return view
}

// report builds one observer's statement about a unit.
func report(machine, service, observer string, epoch, sequence uint64, status healthview.Status) healthview.Observation {
	return healthview.Observation{
		Unit:         healthview.UnitKey{Machine: machine, Service: service},
		ObserverRole: observer,
		Epoch:        epoch,
		Sequence:     sequence,
		Status:       status,
		CheckedAtUTC: time.Date(2026, 7, 29, 11, 59, 58, 0, time.UTC),
		Latency:      4 * time.Millisecond,
	}
}

// unitByService finds one unit in a snapshot.
func unitByService(t *testing.T, snapshot healthview.Snapshot, service string) healthview.UnitSnapshot {
	t.Helper()
	for _, unit := range snapshot.Units {
		if unit.Service == service {
			return unit
		}
	}
	require.FailNow(t, "no unit named "+service+" in the snapshot")
	return healthview.UnitSnapshot{}
}

// TestNewStartsFromTheInventoryWithEverythingUnknown checks a process that has
// heard nothing still knows what exists.
//
// This is what makes a site nobody has reported on distinguishable from a site
// with no services in it. Both would be an empty answer if the view started
// from an empty map and grew as reports arrived.
func TestNewStartsFromTheInventoryWithEverythingUnknown(t *testing.T) {
	view := newView(t, newTestClock(), redundantUnit("sensor", "alarm-service"), soloUnit("gateway", "gateway-services"))

	snapshot := view.Snapshot()
	require.Equal(t, deployment, snapshot.Deployment)
	require.Len(t, snapshot.Units, 2)
	require.Equal(t, healthview.Summary{Unknown: 2}, snapshot.Summary)

	alarm := unitByService(t, snapshot, "alarm-service")
	require.Equal(t, healthview.StatusUnknown, alarm.Status)
	require.Equal(t, []string{"primary", "standby"}, alarm.ExpectedObservers)
	require.Equal(t, []string{"primary", "standby"}, alarm.MissingObservers,
		"an expected observer that has never reported is missing, not stale")
	require.Empty(t, alarm.StaleObservers)
	require.Empty(t, alarm.Observations)
}

// TestSnapshotOrderFollowsTheInventory checks two instances built from the same
// descriptor render the same snapshot.
//
// Convergence is only assertable if the rendering is deterministic. The unit
// order is the inventory's, and observers are sorted, so nothing about a
// snapshot depends on the order reports happened to arrive in.
func TestSnapshotOrderFollowsTheInventory(t *testing.T) {
	inventory := []healthview.Unit{
		redundantUnit("sensor", "alarm-service"),
		soloUnit("gateway", "gateway-services"),
		redundantUnit("sensor", "core-services"),
	}
	clock := newTestClock()

	first := newView(t, clock, inventory...)
	second := newView(t, clock, inventory...)

	// The same reports, applied in opposite orders.
	reports := []healthview.Observation{
		report("sensor", "alarm-service", "primary", 1, 1, healthview.StatusHealthy),
		report("gateway", "gateway-services", "primary", 1, 1, healthview.StatusUnhealthy),
		report("sensor", "core-services", "standby", 1, 1, healthview.StatusHealthy),
	}
	for _, observation := range reports {
		require.Equal(t, healthview.DropNone, first.Apply(observation))
	}
	for i := len(reports) - 1; i >= 0; i-- {
		require.Equal(t, healthview.DropNone, second.Apply(reports[i]))
	}

	require.Equal(t, first.Snapshot(), second.Snapshot(),
		"the same reports reduce to the same view whatever order they arrived in")
	require.Equal(t, []string{"alarm-service", "gateway-services", "core-services"},
		[]string{first.Snapshot().Units[0].Service, first.Snapshot().Units[1].Service, first.Snapshot().Units[2].Service})
}

// TestReductionOfFreshObservers is the aggregation table.
//
// Agreement is that answer, disagreement is Degraded rather than resolved
// either way, and an observer that has not decided contributes nothing. The
// last of those is the rule that keeps a starting instance from dragging a
// service's answer around: its first probes report unknown, and that must
// neither agree with a healthy peer nor disagree with one.
func TestReductionOfFreshObservers(t *testing.T) {
	tests := map[string]struct {
		primary  *healthview.Status
		standby  *healthview.Status
		want     healthview.Status
		wantWhy  string
		wantMiss []string
	}{
		"nobody has reported": {
			want: healthview.StatusUnknown, wantWhy: "the platform has no basis for a claim",
			wantMiss: []string{"primary", "standby"},
		},
		"both healthy": {
			primary: status(healthview.StatusHealthy), standby: status(healthview.StatusHealthy),
			want: healthview.StatusHealthy, wantMiss: []string{},
		},
		"both unhealthy": {
			primary: status(healthview.StatusUnhealthy), standby: status(healthview.StatusUnhealthy),
			want: healthview.StatusUnhealthy, wantMiss: []string{},
		},
		"they disagree": {
			primary: status(healthview.StatusHealthy), standby: status(healthview.StatusUnhealthy),
			want:     healthview.StatusDegraded,
			wantWhy:  "one observer seeing a service another cannot is a real condition, not one to resolve",
			wantMiss: []string{},
		},
		"one has reported and the other has not": {
			primary:  status(healthview.StatusHealthy),
			want:     healthview.StatusHealthy,
			wantWhy:  "a silent peer does not erase a live observation",
			wantMiss: []string{"standby"},
		},
		"one is undecided and the other found it healthy": {
			primary: status(healthview.StatusHealthy), standby: status(healthview.StatusUnknown),
			want:     healthview.StatusHealthy,
			wantWhy:  "an observer that has not resolved the service is not disagreeing with one that has",
			wantMiss: []string{},
		},
		"both are undecided": {
			primary: status(healthview.StatusUnknown), standby: status(healthview.StatusUnknown),
			want:     healthview.StatusUnknown,
			wantWhy:  "present observers with no finding are still no finding",
			wantMiss: []string{},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			view := newView(t, newTestClock(), redundantUnit("sensor", "alarm-service"))
			if test.primary != nil {
				require.Equal(t, healthview.DropNone,
					view.Apply(report("sensor", "alarm-service", "primary", 1, 1, *test.primary)))
			}
			if test.standby != nil {
				require.Equal(t, healthview.DropNone,
					view.Apply(report("sensor", "alarm-service", "standby", 1, 1, *test.standby)))
			}

			unit := unitByService(t, view.Snapshot(), "alarm-service")
			require.Equal(t, test.want, unit.Status, test.wantWhy)
			require.Equal(t, test.wantMiss, unit.MissingObservers)
		})
	}
}

func status(s healthview.Status) *healthview.Status { return &s }

// TestObservationsExpireByArrivalRatherThanBySenderClock checks freshness is the
// receiver's.
//
// Machines at a site do not share a clock. A sender with a wrong one would
// otherwise be able to make its reports immortal or stillborn, and the site's
// picture would depend on the worst clock in it.
func TestObservationsExpireByArrivalRatherThanBySenderClock(t *testing.T) {
	clock := newTestClock()
	view := newView(t, clock, soloUnit("sensor", "alarm-service"))

	// A report whose sender claims it probed a year ago. It is current here,
	// because it arrived here now.
	stale := report("sensor", "alarm-service", "primary", 1, 1, healthview.StatusHealthy)
	stale.CheckedAtUTC = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	require.Equal(t, healthview.DropNone, view.Apply(stale))

	unit := unitByService(t, view.Snapshot(), "alarm-service")
	require.Equal(t, healthview.StatusHealthy, unit.Status,
		"what the sender's clock said decides nothing")
	require.Empty(t, unit.StaleObservers)
	require.Equal(t, stale.CheckedAtUTC, unit.Observations[0].CheckedAtUTC,
		"the sender's time is kept for a reader")

	// Now let it age out here.
	clock.advance(freshFor + time.Second)
	unit = unitByService(t, view.Snapshot(), "alarm-service")
	require.Equal(t, healthview.StatusUnknown, unit.Status,
		"an expired report is no longer a basis for a claim")
	require.Equal(t, []string{"primary"}, unit.StaleObservers)
	require.Empty(t, unit.MissingObservers,
		"an observer that stopped talking is stale, not missing; they are different faults")
	require.True(t, unit.Observations[0].Stale)
	require.Equal(t, healthview.StatusHealthy, unit.Observations[0].Status,
		"what it last said is kept and shown: it is still the most recent thing anyone knows")
}

// TestFreshnessBoundIsInclusive pins the edge, so two receivers with the same
// reports never straddle it.
func TestFreshnessBoundIsInclusive(t *testing.T) {
	clock := newTestClock()
	view := newView(t, clock, soloUnit("sensor", "alarm-service"))
	require.Equal(t, healthview.DropNone,
		view.Apply(report("sensor", "alarm-service", "primary", 1, 1, healthview.StatusHealthy)))

	clock.advance(freshFor)
	require.Equal(t, healthview.StatusHealthy,
		unitByService(t, view.Snapshot(), "alarm-service").Status,
		"a report exactly at its bound is still current")

	clock.advance(time.Nanosecond)
	require.Equal(t, healthview.StatusUnknown,
		unitByService(t, view.Snapshot(), "alarm-service").Status)
}

// TestFencingAppliesOnlyNewerReports covers duplicate suppression and ordering.
//
// Delivery is at-most-once with no ordering promise, so a message can arrive
// twice or out of order. Neither may undo newer state, and a restarted
// observer's first report must supersede everything its previous incarnation
// said.
func TestFencingAppliesOnlyNewerReports(t *testing.T) {
	view := newView(t, newTestClock(), soloUnit("sensor", "alarm-service"))
	current := report("sensor", "alarm-service", "primary", 2, 5, healthview.StatusHealthy)
	require.Equal(t, healthview.DropNone, view.Apply(current))

	tests := map[string]struct {
		epoch, sequence uint64
		want            healthview.DropReason
	}{
		"the same message again":       {epoch: 2, sequence: 5, want: healthview.DropDuplicate},
		"an earlier sequence":          {epoch: 2, sequence: 4, want: healthview.DropStale},
		"an earlier epoch":             {epoch: 1, sequence: 99, want: healthview.DropStale},
		"a later sequence":             {epoch: 2, sequence: 6, want: healthview.DropNone},
		"a later epoch with a low seq": {epoch: 3, sequence: 1, want: healthview.DropNone},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			view := newView(t, newTestClock(), soloUnit("sensor", "alarm-service"))
			require.Equal(t, healthview.DropNone, view.Apply(current))

			got := view.Apply(report("sensor", "alarm-service", "primary", test.epoch, test.sequence, healthview.StatusUnhealthy))
			require.Equal(t, test.want, got)

			unit := unitByService(t, view.Snapshot(), "alarm-service")
			if test.want == healthview.DropNone {
				require.Equal(t, healthview.StatusUnhealthy, unit.Status, "a newer report replaces what the slot held")
				return
			}
			require.Equal(t, healthview.StatusHealthy, unit.Status, "an older or repeated report cannot undo newer state")
		})
	}
	_ = view
}

// TestRestartedObserverSupersedesItsPreviousIncarnation is the reason each
// process captures a durable epoch after its process-start advance.
//
// A restarted observer begins its sequence again. Without the epoch its first
// report would look older than what its previous incarnation last said, and the
// site would hold a dead process's opinion until the sequence caught up.
func TestRestartedObserverSupersedesItsPreviousIncarnation(t *testing.T) {
	view := newView(t, newTestClock(), soloUnit("sensor", "alarm-service"))
	require.Equal(t, healthview.DropNone,
		view.Apply(report("sensor", "alarm-service", "primary", 4, 900, healthview.StatusHealthy)))

	require.Equal(t, healthview.DropNone,
		view.Apply(report("sensor", "alarm-service", "primary", 5, 1, healthview.StatusUnhealthy)),
		"a new incarnation's first report is newer than everything the last one said")
	require.Equal(t, healthview.StatusUnhealthy,
		unitByService(t, view.Snapshot(), "alarm-service").Status)
}

// TestApplyRejectsWhatTheViewCannotPlace covers the drops the view alone can
// see, and that each is counted.
//
// A view quietly discarding traffic looks exactly like a site that has gone
// quiet. The counters are what tell an operator which one they are looking at.
func TestApplyRejectsWhatTheViewCannotPlace(t *testing.T) {
	tests := map[string]struct {
		observation healthview.Observation
		want        healthview.DropReason
	}{
		"a unit this site does not contain": {
			observation: report("elsewhere", "alarm-service", "primary", 1, 1, healthview.StatusHealthy),
			want:        healthview.DropUnknownTarget,
		},
		"a service this machine does not host": {
			observation: report("sensor", "other-services", "primary", 1, 1, healthview.StatusHealthy),
			want:        healthview.DropUnknownTarget,
		},
		"an observer that is not expected": {
			observation: report("sensor", "alarm-service", "standby", 1, 1, healthview.StatusHealthy),
			want:        healthview.DropUnknownObserver,
		},
		"a status only reduction may produce": {
			observation: report("sensor", "alarm-service", "primary", 1, 1, healthview.StatusDegraded),
			want:        healthview.DropUnusableStatus,
		},
		"a status that is not one of ours": {
			observation: report("sensor", "alarm-service", "primary", 1, 1, healthview.Status("fine")),
			want:        healthview.DropUnusableStatus,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			view := newView(t, newTestClock(), soloUnit("sensor", "alarm-service"))
			require.Equal(t, test.want, view.Apply(test.observation))
			require.Equal(t, uint64(1), view.Drops()[test.want], "the drop was counted")
			require.Equal(t, healthview.StatusUnknown,
				unitByService(t, view.Snapshot(), "alarm-service").Status,
				"nothing about the unit changed")
		})
	}
}

// TestSnapshotCarriesEveryDropCounter checks a snapshot reports all the reasons,
// including the ones at zero, so a reader sees the whole set rather than only
// what has gone wrong so far.
func TestSnapshotCarriesEveryDropCounter(t *testing.T) {
	view := newView(t, newTestClock(), soloUnit("sensor", "alarm-service"))
	view.Apply(report("elsewhere", "alarm-service", "primary", 1, 1, healthview.StatusHealthy))

	drops := view.Snapshot().Drops
	require.Equal(t, uint64(1), drops[healthview.DropUnknownTarget])
	require.Contains(t, drops, healthview.DropDuplicate)
	require.Equal(t, uint64(0), drops[healthview.DropDuplicate])
}

// TestNewRejectsAnUnusableInventory checks a view that could not answer for its
// site is not built.
func TestNewRejectsAnUnusableInventory(t *testing.T) {
	unit := redundantUnit("sensor", "alarm-service")
	tests := map[string]struct {
		deployment healthview.Deployment
		inventory  []healthview.Unit
		clock      healthview.Clock
		errText    string
	}{
		"no site": {
			deployment: healthview.Deployment{Project: "customer-a", Environment: "production"},
			inventory:  []healthview.Unit{unit}, clock: newTestClock(), errText: "site is required",
		},
		"no clock": {
			deployment: deployment, inventory: []healthview.Unit{unit}, errText: "a clock is required",
		},
		"a unit with no machine": {
			deployment: deployment, clock: newTestClock(),
			inventory: []healthview.Unit{func() healthview.Unit { u := unit; u.Machine = ""; return u }()},
			errText:   "has no machine",
		},
		"a unit nobody observes": {
			deployment: deployment, clock: newTestClock(),
			inventory: []healthview.Unit{func() healthview.Unit { u := unit; u.ObserverRoles = nil; return u }()},
			errText:   "at least one observer role is required",
		},
		"a unit lists one observer twice": {
			deployment: deployment, clock: newTestClock(),
			inventory: []healthview.Unit{func() healthview.Unit {
				u := unit
				u.ObserverRoles = []string{"primary", "primary"}
				return u
			}()},
			errText: "listed more than once",
		},
		"a unit whose reports never expire": {
			deployment: deployment, clock: newTestClock(),
			inventory: []healthview.Unit{func() healthview.Unit { u := unit; u.FreshFor = 0; return u }()},
			errText:   "must be positive",
		},
		"one unit listed twice": {
			deployment: deployment, clock: newTestClock(),
			inventory: []healthview.Unit{unit, unit},
			errText:   "in the inventory more than once",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			view, err := healthview.New(test.deployment, test.inventory, test.clock)
			require.ErrorContains(t, err, test.errText)
			require.Nil(t, view)
		})
	}
}

func TestViewOwnsItsObserverPolicy(t *testing.T) {
	clock := newTestClock()
	unit := redundantUnit("sensor", "alarm-service")
	view := newView(t, clock, unit)

	unit.ObserverRoles[0] = "tampered"

	snapshot := view.Snapshot().Units[0]
	require.Equal(t, []string{"primary", "standby"}, snapshot.ExpectedObservers)
	require.Equal(t, []string{"primary", "standby"}, snapshot.MissingObservers)
}

// TestViewIsSafeForConcurrentUse checks the lock holds under the shape a running
// process produces: reports arriving off a transport, this machine's own probes
// applying theirs, and the API reading snapshots.
func TestViewIsSafeForConcurrentUse(t *testing.T) {
	view := newView(t, newTestClock(), redundantUnit("sensor", "alarm-service"), soloUnit("gateway", "gateway-services"))

	var workers sync.WaitGroup
	for _, observer := range []string{"primary", "standby"} {
		workers.Go(func() {
			for sequence := uint64(1); sequence <= 200; sequence++ {
				view.Apply(report("sensor", "alarm-service", observer, 1, sequence, healthview.StatusHealthy))
			}
		})
	}
	workers.Go(func() {
		for sequence := uint64(1); sequence <= 200; sequence++ {
			view.Apply(report("gateway", "gateway-services", "primary", 1, sequence, healthview.StatusUnhealthy))
		}
	})
	workers.Go(func() {
		for range 200 {
			_ = view.Snapshot()
			_ = view.Drops()
		}
	})
	workers.Wait()

	snapshot := view.Snapshot()
	require.Equal(t, healthview.StatusHealthy, unitByService(t, snapshot, "alarm-service").Status)
	require.Equal(t, healthview.StatusUnhealthy, unitByService(t, snapshot, "gateway-services").Status)
}
