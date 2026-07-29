package healthview

import (
	"slices"
	"time"
)

// Snapshot is the whole site as this instance sees it at one moment.
//
// It is a value, copied out under the lock, so a reader holds a consistent
// picture rather than a live map that changes while it renders. Everything in
// it is ordered deterministically: two instances that received the same reports
// produce byte-identical snapshots, which is what makes convergence something a
// test can assert rather than describe.
type Snapshot struct {
	// Deployment is the site this is a picture of.
	Deployment Deployment
	// GeneratedAtUTC is when the snapshot was taken.
	GeneratedAtUTC time.Time
	// Units are every service the inventory contains, in inventory order.
	Units []UnitSnapshot
	// Summary counts the units by status.
	Summary Summary
	// Drops is how many observations this view has discarded, by reason. It is
	// here because a view quietly discarding traffic looks exactly like a site
	// that has gone quiet, and only this tells the two apart.
	Drops map[DropReason]uint64
}

// Summary counts units by their reduced status.
type Summary struct {
	Healthy   int
	Unhealthy int
	Degraded  int
	Unknown   int
}

// UnitSnapshot is one service and what is currently known about it.
type UnitSnapshot struct {
	Machine        string
	MachineProfile string
	Service        string
	ServiceRole    string
	// Status is the reduction of every current observation below.
	Status Status
	// ExpectedObservers are the platform instance roles that should be reporting.
	ExpectedObservers []string
	// MissingObservers are expected observers that have never reported.
	MissingObservers []string
	// StaleObservers are expected observers whose last report has expired. They
	// are listed apart from the missing ones because an observer that stopped
	// talking and one that never started are different faults.
	StaleObservers []string
	// Observations are what each observer last said, current or not, ordered by
	// observer role.
	Observations []ObserverSnapshot
}

// ObserverSnapshot is one observer's last report about one unit.
type ObserverSnapshot struct {
	ObserverRole string
	Status       Status
	// Stale reports whether this observation has passed the unit's freshness
	// bound. A stale observation is kept and shown rather than deleted: what it
	// last said is the most recent thing anyone knows, and that it has aged is a
	// separate fact.
	Stale bool
	// CheckedAtUTC is when the sender says it probed, and ReceivedAtUTC is when
	// this process heard about it. Both are shown because a large gap between
	// them is either a slow site or a machine whose clock is wrong, and an
	// operator cannot tell which from either one alone.
	CheckedAtUTC  time.Time
	ReceivedAtUTC time.Time
	// Age is how long ago this report arrived, on the receiver's clock.
	Age time.Duration
	// Latency is how long the sender's own attempt took.
	Latency time.Duration
	// ConsecutiveFailures is how many attempts had failed in a row when the
	// sender reported.
	ConsecutiveFailures int
	// Error is why the sender's attempt failed, empty when it succeeded.
	Error string
}

// Snapshot renders the whole view.
//
// Freshness is evaluated here rather than when a report arrives, so a snapshot
// reflects what is current at the moment it is read. Nothing expires in the
// background: a report that nobody looked at while it aged out did not need to
// be swept, and a timer that swept it would be a second thing to get wrong.
func (v *View) Snapshot() Snapshot {
	v.mu.Lock()
	defer v.mu.Unlock()

	now := v.clock.Now()
	snapshot := Snapshot{
		Deployment:     v.deployment,
		GeneratedAtUTC: now.UTC(),
		Units:          make([]UnitSnapshot, 0, len(v.order)),
		Drops:          make(map[DropReason]uint64, len(dropReasons)),
	}
	for _, reason := range dropReasons {
		snapshot.Drops[reason] = v.drops[reason]
	}
	for _, key := range v.order {
		unit := v.renderUnit(v.units[key], now)
		switch unit.Status {
		case StatusHealthy:
			snapshot.Summary.Healthy++
		case StatusUnhealthy:
			snapshot.Summary.Unhealthy++
		case StatusDegraded:
			snapshot.Summary.Degraded++
		case StatusUnknown:
			snapshot.Summary.Unknown++
		}
		snapshot.Units = append(snapshot.Units, unit)
	}
	return snapshot
}

// renderUnit reduces one unit's reports into an answer and describes how it got
// there. It is called with the lock held.
func (v *View) renderUnit(state *unitState, now time.Time) UnitSnapshot {
	unit := UnitSnapshot{
		Machine:        state.unit.Machine,
		MachineProfile: state.unit.MachineProfile,
		Service:        state.unit.Service,
		ServiceRole:    state.unit.ServiceRole,
		// Sorted rather than carried in inventory order, so the same set of roles
		// always renders the same way whatever order the descriptor listed them.
		ExpectedObservers: slices.Sorted(slices.Values(state.unit.ObserverRoles)),
		MissingObservers:  []string{},
		StaleObservers:    []string{},
		Observations:      make([]ObserverSnapshot, 0, len(state.observed)),
	}

	var verdicts []Status
	for _, role := range unit.ExpectedObservers {
		reported, ok := state.observed[role]
		if !ok {
			unit.MissingObservers = append(unit.MissingObservers, role)
			continue
		}
		age := now.Sub(reported.receivedAt)
		stale := age > state.unit.FreshFor
		if stale {
			unit.StaleObservers = append(unit.StaleObservers, role)
		} else if reported.status.verdict() {
			// Only current reports carrying a finding count towards the answer. An
			// observer that has not resolved the service yet is present and silent
			// on the question, which is neither agreement nor disagreement.
			verdicts = append(verdicts, reported.status)
		}
		unit.Observations = append(unit.Observations, ObserverSnapshot{
			ObserverRole:        role,
			Status:              reported.status,
			Stale:               stale,
			CheckedAtUTC:        reported.checkedAtUTC.UTC(),
			ReceivedAtUTC:       reported.receivedAt.UTC(),
			Age:                 age,
			Latency:             reported.latency,
			ConsecutiveFailures: reported.consecutiveFailures,
			Error:               reported.failure,
		})
	}
	unit.Status = reduce(verdicts)
	return unit
}

// reduce turns the current findings about one service into one answer.
//
// Nothing current means Unknown: the platform has no basis for a claim, and
// saying either Healthy or Unhealthy would invent one. Findings that agree are
// that finding. Findings that disagree are Degraded, which is deliberately not
// resolved in either direction — one observer seeing a service another cannot
// is a real condition, and choosing the Active instance's answer or the newest
// message would hide it behind a decision the platform is not entitled to make.
func reduce(verdicts []Status) Status {
	if len(verdicts) == 0 {
		return StatusUnknown
	}
	first := verdicts[0]
	for _, verdict := range verdicts[1:] {
		if verdict != first {
			return StatusDegraded
		}
	}
	return first
}
