package harness

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// This file is the scenario side of GET /health/services.
//
// Like the rest of platformapi.go it re-declares the JSON shapes rather than
// importing the platform's Go types, so a scenario checks the published contract
// and a change to it shows up here as a failing scenario rather than as a
// silently recompiled struct.

// The service statuses the platform publishes.
const (
	// ServiceUnknown is a service nothing current says anything about.
	ServiceUnknown = "Unknown"
	// ServiceHealthy is a service every current observer found well.
	ServiceHealthy = "Healthy"
	// ServiceUnhealthy is a service every current observer found failing.
	ServiceUnhealthy = "Unhealthy"
	// ServiceDegraded is a service whose current observers disagree.
	ServiceDegraded = "Degraded"
)

// The states an instance reports for its half of the site's health traffic.
const (
	// DistributionConnected means every expected observer elsewhere is current.
	DistributionConnected = "Connected"
	// DistributionLocal means there is no observer elsewhere to hear from.
	DistributionLocal = "Local"
)

// ServiceView is the observable shape of GET /health/services: one instance's
// whole view of the site's deployed services.
type ServiceView struct {
	Project        string              `json:"project"`
	Environment    string              `json:"environment"`
	Site           string              `json:"site"`
	Machine        string              `json:"machine"`
	Role           string              `json:"role"`
	GeneratedAtUTC string              `json:"generatedAtUtc"`
	Summary        ServiceSummary      `json:"summary"`
	Services       []ServiceUnit       `json:"services"`
	Distribution   ServiceDistribution `json:"distribution"`
}

// ServiceSummary counts the site's services by status.
type ServiceSummary struct {
	Healthy   int `json:"healthy"`
	Unhealthy int `json:"unhealthy"`
	Degraded  int `json:"degraded"`
	Unknown   int `json:"unknown"`
}

// ServiceUnit is one deployed service and what is currently known about it.
type ServiceUnit struct {
	Machine           string               `json:"machine"`
	MachineProfile    string               `json:"machineProfile"`
	Service           string               `json:"service"`
	ServiceRole       string               `json:"serviceRole"`
	Status            string               `json:"status"`
	ExpectedObservers []string             `json:"expectedObservers"`
	MissingObservers  []string             `json:"missingObservers"`
	StaleObservers    []string             `json:"staleObservers"`
	Observations      []ServiceObservation `json:"observations"`
}

// ServiceObservation is one observer's last report about one service.
type ServiceObservation struct {
	ObserverRole        string `json:"observerRole"`
	Status              string `json:"status"`
	Stale               bool   `json:"stale"`
	CheckedAtUTC        string `json:"checkedAtUtc"`
	ReceivedAtUTC       string `json:"receivedAtUtc"`
	AgeMs               int64  `json:"ageMs"`
	LatencyMs           int64  `json:"latencyMs"`
	ConsecutiveFailures int    `json:"consecutiveFailures"`
	Error               string `json:"error,omitempty"`
}

// ServiceDistribution is what an instance's half of the health traffic is doing.
type ServiceDistribution struct {
	State         string          `json:"state"`
	Published     int64           `json:"published"`
	Superseded    int64           `json:"superseded"`
	PublishFailed int64           `json:"publishFailed"`
	Delivered     int64           `json:"delivered"`
	Rejected      []ServiceReason `json:"rejected"`
	Dropped       []ServiceReason `json:"dropped"`
}

// ServiceReason is one reason a message was not applied and how often.
type ServiceReason struct {
	Reason string `json:"reason"`
	Count  int64  `json:"count"`
}

// Unit returns the view's entry for one machine's service, and whether the view
// has one at all.
func (v ServiceView) Unit(machine, service string) (ServiceUnit, bool) {
	for _, unit := range v.Services {
		if unit.Machine == machine && unit.Service == service {
			return unit, true
		}
	}
	return ServiceUnit{}, false
}

// Observation returns one observer's last report about this service, and
// whether that observer has reported at all.
func (u ServiceUnit) Observation(observerRole string) (ServiceObservation, bool) {
	for _, observed := range u.Observations {
		if observed.ObserverRole == observerRole {
			return observed, true
		}
	}
	return ServiceObservation{}, false
}

// Fresh reports whether an observer has reported on this service and that report
// has not expired. It is the condition most waits are written against: an
// observer that never spoke and one that has gone quiet are different faults,
// and neither of them is a current report.
func (u ServiceUnit) Fresh(observerRole string) bool {
	observed, reported := u.Observation(observerRole)
	return reported && !observed.Stale
}

// String renders one service compactly, for a failure message.
func (u ServiceUnit) String() string {
	described := make([]string, 0, len(u.Observations))
	for _, observed := range u.Observations {
		state := observed.Status
		if observed.Stale {
			state += "(stale)"
		}
		described = append(described, observed.ObserverRole+"="+state)
	}
	rendered := fmt.Sprintf("%s/%s %s [%s]", u.Machine, u.Service, u.Status, strings.Join(described, " "))
	if len(u.MissingObservers) > 0 {
		rendered += " missing:" + strings.Join(u.MissingObservers, ",")
	}
	if len(u.StaleObservers) > 0 {
		rendered += " stale:" + strings.Join(u.StaleObservers, ",")
	}
	return rendered
}

// String renders a whole view compactly, for a failure message.
func (v ServiceView) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s/%s view of %s (distribution %s, delivered %d, published %d)",
		v.Machine, v.Role, v.Site, v.Distribution.State, v.Distribution.Delivered, v.Distribution.Published)
	for _, unit := range v.Services {
		fmt.Fprintf(&b, "\n    %s", unit)
	}
	return b.String()
}

// FetchServiceViewAt returns the service view served at baseURL, the status
// code, and any transport failure. Like the rest of this API it reports rather
// than asserts, so it is safe inside a poll.
func FetchServiceViewAt(ctx context.Context, baseURL string) (ServiceView, int, error) {
	return getJSON[ServiceView](ctx, baseURL+"/health/services")
}

// Observer is one running platform instance a scenario watches the site through.
//
// A scenario asks the same questions of every one of them, because the whole
// claim being tested is that they converge: an assertion made against one
// instance proves nothing about a site that has stopped agreeing with itself.
type Observer struct {
	// Name is how a failure message refers to it, "node-a/primary".
	Name string
	// URL is the instance's own API base URL.
	URL string
	// Machine and Role are what it should report about itself.
	Machine string
	Role    string
}

// Observers returns every instance of these machines that serves an API,
// primaries and standbys alike, in a fixed order.
//
// Both, because both observe. A Standby Instance probes its machine's services
// and publishes what it finds for its whole lifetime, whatever the ownership
// state, and it answers this endpoint from its own address the entire time.
func Observers(machines ...*Machine) []*Observer {
	observers := make([]*Observer, 0, 2*len(machines))
	for _, m := range machines {
		observers = append(observers, &Observer{
			Name: m.Name + "/" + RolePrimary, URL: m.URL, Machine: m.Name, Role: RolePrimary,
		})
		if m.StandbyURL != "" {
			observers = append(observers, &Observer{
				Name: m.Name + "/" + RoleStandby, URL: m.StandbyURL, Machine: m.Name, Role: RoleStandby,
			})
		}
	}
	return observers
}

// serviceViewPollInterval is how often a health wait re-asks an instance. It is
// shorter than the scenario probe interval, so a wait sees a transition rather
// than the state after it.
const serviceViewPollInterval = 100 * time.Millisecond

// ServiceViewTimeout bounds a wait for the site to reach a state.
//
// It is generous relative to the fast probe policy the health fixture is
// authored with: convergence there takes about a second, so a wait that runs out
// is a site that is not converging rather than one that is slow.
const ServiceViewTimeout = 45 * time.Second

// WaitForServiceViews blocks until every observer's view satisfies want.
//
// The condition is evaluated against each instance's own answer, and all of them
// must hold at once. That is what "the site converged" means here, and it is
// deliberately not "the instance I happened to ask agrees with me".
func WaitForServiceViews(ctx context.Context, t *testing.T, what string, observers []*Observer, machines []*Machine, want func(ServiceView) bool) {
	t.Helper()
	var last lastViews
	cond := func() bool {
		seen := make([]string, 0, len(observers))
		satisfied := true
		for _, observer := range observers {
			view, code, err := FetchServiceViewAt(ctx, observer.URL)
			switch {
			case err != nil:
				seen = append(seen, observer.Name+": "+err.Error())
				satisfied = false
			case code != http.StatusOK:
				seen = append(seen, fmt.Sprintf("%s: HTTP %d", observer.Name, code))
				satisfied = false
			default:
				seen = append(seen, view.String())
				satisfied = satisfied && want(view)
			}
		}
		last.record(seen)
		return satisfied
	}
	diag := DiagStringer(func() string {
		return last.String() + "\n" + Diagnose(machines)
	})
	WaitFor(t, what, ServiceViewTimeout, serviceViewPollInterval, cond, nil, diag)
}

// GetServiceView returns one instance's view, failing the scenario if it will
// not answer. It is for the assertions a scenario makes once a wait has already
// established that the site is where it should be.
func GetServiceView(ctx context.Context, t *testing.T, observer *Observer, machines ...*Machine) ServiceView {
	t.Helper()
	view, code, err := FetchServiceViewAt(ctx, observer.URL)
	require.NoErrorf(t, err, "%s did not answer for its service view:%s", observer.Name, Diagnostics(machines...))
	require.Equalf(t, http.StatusOK, code, "%s answered HTTP %d for its service view", observer.Name, code)
	return view
}

// lastViews is what the most recently completed poll saw from every observer.
// It is mutex-free by construction: the poll condition and the failure message
// that reads it run on the same goroutine as WaitFor's caller.
type lastViews struct {
	seen []string
}

func (l *lastViews) record(seen []string) { l.seen = seen }

func (l *lastViews) String() string {
	if len(l.seen) == 0 {
		return "no poll ever completed"
	}
	return "--- last service views ---\n" + strings.Join(l.seen, "\n")
}

// SameServiceStatuses reports whether every observer answers with the same
// service statuses, and returns what each of them said.
//
// It compares the reduced status per service and nothing else. Ages, latencies,
// and sequence numbers legitimately differ between two instances that agree
// completely, and asserting on them would make convergence untestable.
func SameServiceStatuses(views []ServiceView) (same bool, byObserver []string) {
	rendered := make([]string, 0, len(views))
	for _, view := range views {
		statuses := make([]string, 0, len(view.Services))
		for _, unit := range view.Services {
			statuses = append(statuses, unit.Machine+"/"+unit.Service+"="+unit.Status)
		}
		sort.Strings(statuses)
		rendered = append(rendered, strings.Join(statuses, " "))
	}
	agreed := len(rendered) > 0 && !slices.ContainsFunc(rendered, func(s string) bool { return s != rendered[0] })
	return agreed, rendered
}
