package healthview

import (
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"
)

// View is this process's picture of every service at its site.
//
// It is safe for concurrent use. One goroutine delivers what arrives off the
// transport, another delivers what this machine's own probes found, and the API
// reads snapshots from a third, so the lock is not an optimisation to remove
// later.
type View struct {
	deployment Deployment
	clock      Clock
	// order is the inventory order, kept so two processes built from the same
	// descriptor render their snapshots identically.
	order []UnitKey

	mu    sync.Mutex
	units map[UnitKey]*unitState
	drops map[DropReason]uint64
}

// unitState is one inventory entry and whatever its expected observers have
// most recently said.
type unitState struct {
	unit Unit
	// observed is keyed by observer role. A role with no entry has never
	// reported; a role whose entry has expired is stale. The two are different
	// facts and stay so.
	observed map[string]report
}

// report is one observer's slot: the newest thing it has said, and when that
// reached us.
type report struct {
	epoch      uint64
	sequence   uint64
	status     Status
	receivedAt time.Time
	// The rest is what the sender said about its own attempt, carried through
	// for a reader and used for nothing here.
	checkedAtUTC        time.Time
	latency             time.Duration
	consecutiveFailures int
	failure             string
}

// New builds a view of the given inventory, with every unit Unknown.
//
// Starting from the inventory rather than from an empty map is the whole point.
// A process that had just started would otherwise be unable to tell a site
// where nothing has reported yet from a site with no services in it, and an
// operator asking either one would get an empty answer.
func New(deployment Deployment, inventory []Unit, clock Clock) (*View, error) {
	if err := deployment.Validate(); err != nil {
		return nil, fmt.Errorf("health view: %w", err)
	}
	if clock == nil {
		return nil, fmt.Errorf("health view: a clock is required")
	}
	view := &View{
		deployment: deployment,
		clock:      clock,
		order:      make([]UnitKey, 0, len(inventory)),
		units:      make(map[UnitKey]*unitState, len(inventory)),
		drops:      make(map[DropReason]uint64, len(dropReasons)),
	}
	for _, unit := range inventory {
		if err := unit.validate(); err != nil {
			return nil, fmt.Errorf("health view: %w", err)
		}
		if _, listed := view.units[unit.UnitKey]; listed {
			return nil, fmt.Errorf("health view: %s is in the inventory more than once", unit.UnitKey)
		}
		// The view owns the policy it reduces against. Keeping the caller's slice
		// would let a later descriptor adapter mutation change expected observers
		// underneath an already-running view.
		unit.ObserverRoles = slices.Clone(unit.ObserverRoles)
		view.order = append(view.order, unit.UnitKey)
		view.units[unit.UnitKey] = &unitState{
			unit:     unit,
			observed: make(map[string]report, len(unit.ObserverRoles)),
		}
	}
	return view, nil
}

// Deployment returns the site this view is of.
func (v *View) Deployment() Deployment { return v.deployment }

// Apply folds one observation in and reports whether it was kept.
//
// It returns DropNone when the observation was applied. Every other answer is a
// reason the view also counted, so a caller can log the first of a kind without
// having to keep its own tally.
//
// It takes no context. Applying is a map write under a mutex, so there is
// nothing here to cancel and nothing that can block long enough to be worth
// cancelling.
func (v *View) Apply(observation Observation) DropReason {
	v.mu.Lock()
	defer v.mu.Unlock()

	state, known := v.units[observation.Unit]
	if !known {
		return v.dropped(DropUnknownTarget)
	}
	if !containsRole(state.unit.ObserverRoles, observation.ObserverRole) {
		return v.dropped(DropUnknownObserver)
	}
	if !slices.Contains(observedStatuses, observation.Status) {
		return v.dropped(DropUnusableStatus)
	}
	// The slot is compared before it is written, so a report that arrived late,
	// twice, or from an observer's previous incarnation cannot undo a newer one.
	if current, reported := state.observed[observation.ObserverRole]; reported {
		switch {
		case observation.Epoch == current.epoch && observation.Sequence == current.sequence:
			return v.dropped(DropDuplicate)
		case observation.Epoch < current.epoch,
			observation.Epoch == current.epoch && observation.Sequence < current.sequence:
			return v.dropped(DropStale)
		}
	}
	state.observed[observation.ObserverRole] = report{
		epoch:    observation.Epoch,
		sequence: observation.Sequence,
		status:   observation.Status,
		// Stamped on arrival, on this process's clock. What the sender wrote is
		// kept below and decides nothing.
		receivedAt:          v.clock.Now(),
		checkedAtUTC:        observation.CheckedAtUTC,
		latency:             observation.Latency,
		consecutiveFailures: observation.ConsecutiveFailures,
		failure:             observation.Error,
	}
	return DropNone
}

// dropped counts a reason and returns it. It is called with the lock held.
func (v *View) dropped(reason DropReason) DropReason {
	v.drops[reason]++
	return reason
}

// Drops returns how many observations were discarded, by reason.
func (v *View) Drops() map[DropReason]uint64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return maps.Clone(v.drops)
}
