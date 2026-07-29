package healthview

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Status is what a service is, either as one observer reported it or as the
// view reduced several reports into one answer.
//
// Three of the four can come off the wire. Degraded cannot: it says observers
// of one service do not agree, which is a statement about a set of reports and
// not something a single observer can make about what it saw.
type Status string

const (
	// StatusUnknown is a service nothing current says anything about: nobody has
	// reported, every report has expired, or the observers that did report had
	// not resolved it yet.
	StatusUnknown Status = "unknown"
	// StatusHealthy is a service every current observer found well.
	StatusHealthy Status = "healthy"
	// StatusUnhealthy is a service every current observer found failing.
	StatusUnhealthy Status = "unhealthy"
	// StatusDegraded is a service whose current observers disagree. It is
	// produced only by reduction.
	StatusDegraded Status = "degraded"
)

// observedStatuses are the statuses an observer may report. Degraded is absent
// deliberately: a receiver that accepted one would be letting a sender state a
// conclusion only the receiver can draw.
var observedStatuses = []Status{StatusUnknown, StatusHealthy, StatusUnhealthy}

// verdict reports whether a status is a finding about the service, as opposed
// to an observer saying it has not resolved one yet.
//
// This is the rule that keeps a starting instance from dragging a service's
// answer around. An observer whose first probes have failed below its retry
// threshold reports unknown, and that must neither count as agreement with a
// healthy peer nor as disagreement with one.
func (s Status) verdict() bool { return s == StatusHealthy || s == StatusUnhealthy }

// Deployment is the site a view belongs to.
//
// It is carried so a report from somewhere else can be told apart from a report
// about something unknown here. Two deployments sharing a broker is a
// misconfiguration rather than a topology, and it should be visible as one.
type Deployment struct {
	Project     string
	Environment string
	Site        string
}

// Validate checks the deployment names a site. It is exported because whatever
// carries observations has to agree with the view about which deployment it is
// serving, and both check the same thing.
func (d Deployment) Validate() error {
	switch {
	case strings.TrimSpace(d.Project) == "":
		return fmt.Errorf("project is required")
	case strings.TrimSpace(d.Environment) == "":
		return fmt.Errorf("environment is required")
	case strings.TrimSpace(d.Site) == "":
		return fmt.Errorf("site is required")
	}
	return nil
}

// UnitKey identifies one deployed service unit within a site.
//
// A service name is unique only within its machine, so the machine is half the
// key. The project, environment, and site are not: one view covers one site and
// checks them on the way in rather than repeating them on every unit.
type UnitKey struct {
	Machine string
	Service string
}

func (k UnitKey) String() string { return k.Machine + "/" + k.Service }

// Unit is one entry of the static inventory: a service that exists at this
// site, who is expected to report on it, and how long one of those reports
// stays current.
type Unit struct {
	UnitKey
	// MachineProfile is the purpose of the machine hosting this unit.
	MachineProfile string
	// ServiceRole is the part this copy plays: "master" or "slave".
	ServiceRole string
	// ObserverRoles are the platform instance roles expected to report on this
	// unit. An expected observer that is silent is a fact about the platform
	// rather than about the service, and this is what makes the two tellable
	// apart.
	ObserverRoles []string
	// FreshFor is how long one report on this unit stays current, measured from
	// when it arrived here.
	FreshFor time.Duration
}

func (u Unit) validate() error {
	switch {
	case strings.TrimSpace(u.Machine) == "":
		return fmt.Errorf("a unit has no machine")
	case strings.TrimSpace(u.Service) == "":
		return fmt.Errorf("a unit has no service")
	}
	where := u.String()
	switch {
	case strings.TrimSpace(u.MachineProfile) == "":
		return fmt.Errorf("%s: machine profile is required", where)
	case strings.TrimSpace(u.ServiceRole) == "":
		return fmt.Errorf("%s: service role is required", where)
	case len(u.ObserverRoles) == 0:
		return fmt.Errorf("%s: at least one observer role is required; a unit nobody reports on is never anything but unknown", where)
	case u.FreshFor <= 0:
		return fmt.Errorf("%s: fresh_for %s must be positive", where, u.FreshFor)
	}
	seenRoles := make(map[string]bool, len(u.ObserverRoles))
	for _, role := range u.ObserverRoles {
		switch {
		case strings.TrimSpace(role) == "":
			return fmt.Errorf("%s: an observer role is blank", where)
		case role != strings.TrimSpace(role):
			return fmt.Errorf("%s: observer role %q must not have leading or trailing whitespace", where, role)
		case seenRoles[role]:
			return fmt.Errorf("%s: observer role %q is listed more than once", where, role)
		}
		seenRoles[role] = true
	}
	return nil
}

// Observation is one observer's report about one unit, as it reached this
// process.
//
// It is a value rather than a wire type. What delivered it, and in what format,
// is the transport's business; by the time it gets here it is a report from a
// named observer about a named unit.
type Observation struct {
	// Unit is what the report is about.
	Unit UnitKey
	// ObserverRole is which of the hosting machine's platform instances reported:
	// "primary" or "standby".
	ObserverRole string
	// Epoch is the durable instance epoch captured at observer process startup,
	// and Sequence orders reports within that process. Together they say whether
	// this report is newer than what that observer's slot already holds.
	Epoch    uint64
	Sequence uint64
	// Status is what the observer found. Degraded is not accepted here.
	Status Status
	// CheckedAtUTC is when the sender says it probed. It is diagnostic only:
	// freshness is measured from arrival, because machines at a site do not share
	// a clock.
	CheckedAtUTC time.Time
	// Latency is how long the sender's attempt took.
	Latency time.Duration
	// ConsecutiveFailures is how many attempts had failed in a row when the
	// sender reported. It is what shows a service on its way down before its
	// status has moved.
	ConsecutiveFailures int
	// Error is why the sender's attempt failed, empty when it succeeded.
	Error string
}

// DropReason says why an observation was not applied. It is the empty string
// when the observation was applied.
//
// Every reason is counted rather than only logged. A view that is quietly
// discarding traffic looks exactly like a site that has gone quiet, and the
// counters are what tell an operator which one they are looking at.
type DropReason string

const (
	// DropNone means the observation was applied.
	DropNone DropReason = ""
	// DropUnknownTarget is a report about a unit this site's inventory does not
	// contain. It is either a machine built from a different blueprint or one
	// left over from an older one.
	DropUnknownTarget DropReason = "unknown_target"
	// DropUnknownObserver is a report from a role that is not expected to
	// observe that unit, such as a standby report about a machine that deploys
	// none.
	DropUnknownObserver DropReason = "unknown_observer"
	// DropUnusableStatus is a report carrying a status an observer may not state,
	// including the reduction-only Degraded.
	DropUnusableStatus DropReason = "unusable_status"
	// DropDuplicate is a report this observer's slot already holds: the same
	// epoch and the same sequence.
	DropDuplicate DropReason = "duplicate"
	// DropStale is a report older than what that slot holds, from an earlier
	// epoch or an earlier sequence within the current one.
	DropStale DropReason = "stale"
)

// dropReasons is every reason a view counts, in the order a report renders
// them, so two processes describe their drops the same way.
var dropReasons = []DropReason{
	DropUnknownTarget,
	DropUnknownObserver,
	DropUnusableStatus,
	DropDuplicate,
	DropStale,
}

// Clock is the passage of time a view measures freshness with.
//
// It is the receiver's own, never the sender's, and it is injected so expiry can
// be tested by moving it rather than by waiting.
type Clock interface {
	Now() time.Time
}

// SystemClock is the real clock, and the one a running process uses.
type SystemClock struct{}

// Now returns the current time, carrying the monotonic reading that makes
// elapsed time immune to a wall-clock adjustment.
func (SystemClock) Now() time.Time { return time.Now() }

// containsRole reports whether roles names role.
func containsRole(roles []string, role string) bool { return slices.Contains(roles, role) }
