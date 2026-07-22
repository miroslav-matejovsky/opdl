package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// This file is the scenario side of the platform's registration API: enough to
// call it over HTTP and read what came back.
//
// It is a black-box view. It re-declares the JSON shapes it needs rather than
// importing the platform's Go types, so a scenario checks the published contract
// and a change to it shows up here as a failing scenario rather than as a
// silently recompiled struct.

const (
	// apiPollInterval is how often a wait retries a request.
	apiPollInterval = 100 * time.Millisecond
	// APIWaitTimeout bounds a wait for the API to answer or to reach a state.
	// It is generous: it covers a machine starting its NATS server, replaying the
	// site journal, and every expected machine deciding on its own schedule.
	APIWaitTimeout = 60 * time.Second
)

// APIPollInterval is how often a wait retries a request. Scenarios that run their
// own wait loops poll on the same cadence the harness does.
const APIPollInterval = apiPollInterval

// ProposalAccepted is the observable shape of a 202: the journal took the
// proposal, and this is the handle to poll.
type ProposalAccepted struct {
	// ProposalID is the proposal's stable identity and its status key.
	ProposalID string `json:"proposal_id"`
	// Sequence is the proposal's position in the site journal.
	Sequence uint64 `json:"sequence"`
}

// Registration is the observable shape of one registration in the API's JSON.
type Registration struct {
	// ProposalID is the proposal's stable identity.
	ProposalID string `json:"proposal_id"`
	// UnitType is the registered unit type identifier.
	UnitType uint8 `json:"unit_type"`
	// UnitID is the registered unit identifier.
	UnitID uint16 `json:"unit_id"`
	// UnitTypeNameAdvertised is the unit's advertised type name.
	UnitTypeNameAdvertised string `json:"unit_type_name_advertised"`
	// Role is the optional advertised role.
	Role *string `json:"role,omitempty"`
	// Machine is the descriptor machine the proposal originated on.
	Machine string `json:"machine"`
	// IP is the descriptor IP of that machine.
	IP string `json:"ip"`
	// Status is pending, accepted, or rejected.
	Status string `json:"status"`
	// Reason is the optional bounded rejection code.
	Reason *string `json:"reason,omitempty"`
	// PlatformInstances is every expected platform instance's progress.
	PlatformInstances []PlatformInstance `json:"platform_instances"`
}

// PlatformInstance is one platform instance's progress for a registration.
type PlatformInstance struct {
	// Machine is the platform instance's descriptor machine.
	Machine string `json:"machine"`
	// IP is the platform instance's descriptor IP.
	IP string `json:"ip"`
	// Status is pending, accepted, or rejected.
	Status string `json:"status"`
	// Reason is the optional bounded rejection code.
	Reason *string `json:"reason,omitempty"`
}

// Conflict is the observable shape of one resolved duplicate claim.
type Conflict struct {
	// UnitType is the unit type identifier shared by the competing proposals.
	UnitType uint8 `json:"unit_type"`
	// UnitID is the unit identifier shared by the competing proposals.
	UnitID uint16 `json:"unit_id"`
	// ResolutionStatus is resolved once the winner is determined.
	ResolutionStatus string `json:"resolution_status"`
	// Winner is the proposal that keeps the unit key.
	Winner Registration `json:"winner"`
	// Losers are the proposals rejected for the key.
	Losers []Registration `json:"losers"`
}

// Instance returns one expected platform instance's entry, failing when the
// projection does not cover that machine at all.
func (r Registration) Instance(t *testing.T, machine string) PlatformInstance {
	t.Helper()
	for _, entry := range r.PlatformInstances {
		if entry.Machine == machine {
			return entry
		}
	}
	require.FailNow(t, "no platform instance entry", "machine %s not in %+v", machine, r.PlatformInstances)
	return PlatformInstance{}
}

// getJSON issues a GET and decodes a 200 body into T. A non-200 is returned as
// a code with no value. Transport failures are returned, never asserted, so
// this is safe to call from inside a poll.
func getJSON[T any](ctx context.Context, url string) (value T, code int, err error) {
	var zero T
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return zero, 0, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return zero, 0, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return zero, response.StatusCode, nil
	}
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		return zero, response.StatusCode, err
	}
	return value, response.StatusCode, nil
}

// postJSON is the same for a JSON request body.
func postJSON[T any](ctx context.Context, url, body string) (value T, code int, err error) {
	var zero T
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return zero, 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return zero, 0, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusAccepted && response.StatusCode != http.StatusOK {
		return zero, response.StatusCode, nil
	}
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		return zero, response.StatusCode, err
	}
	return value, response.StatusCode, nil
}

// SubmitRegistration submits a registration request to one machine and reports
// what came back, including a transport failure as an error rather than as a
// fatal assertion. The proposal is meaningful only on 202.
//
// Reporting rather than asserting is what makes it safe to call from inside a
// require.Eventually condition, and that is not a style preference. testify runs
// the condition on a goroutine of its own and only re-arms its ticker once the
// condition sends a result back. A require.* failing there calls runtime.Goexit,
// so no result is ever sent, the wait silently stops polling, and it burns its
// whole timeout before reporting "Condition never satisfied" — hiding the single
// transport error that was the actual failure.
//
// Note that stage 6 removed the underlying hazard, since waitfor.Poll runs its
// condition on the caller's goroutine. Keep the reporting variants anyway: a
// function that returns an error is the better shape regardless, and the comment
// explaining the history is worth preserving as a warning.
func SubmitRegistration(ctx context.Context, m *Machine, body string) (ProposalAccepted, int, error) {
	return postJSON[ProposalAccepted](ctx, m.URL+"/registrations", body)
}

// fetchRegistration returns one machine's view of a proposal, the status code it
// answered with, and any transport failure. The registration is meaningful only
// on 200. It reports rather than asserts for the reason SubmitRegistration does.
func fetchRegistration(ctx context.Context, m *Machine, proposalID string) (Registration, int, error) {
	return getJSON[Registration](ctx, m.URL+"/registrations/"+proposalID)
}

// Propose submits a registration request and requires the journal to take it,
// which is what a scenario means when it says a client registered something.
func Propose(ctx context.Context, t *testing.T, m *Machine, body string) ProposalAccepted {
	t.Helper()
	accepted, code, err := SubmitRegistration(ctx, m, body)
	require.NoErrorf(t, err, "%s did not answer the proposal:%s", m.Name, Diagnostics(m))
	require.Equalf(t, http.StatusAccepted, code, "%s did not take the proposal:%s", m.Name, Diagnostics(m))
	require.NotEmpty(t, accepted.ProposalID, "202 hands back the handle the client polls with")
	return accepted
}

// GetRegistration returns one machine's view of a proposal, and the status code
// it answered with. A machine that does not answer at all is a failure here, so
// this is for the test goroutine; a poll wants fetchRegistration.
func GetRegistration(ctx context.Context, t *testing.T, m *Machine, proposalID string) (view Registration, code int) {
	t.Helper()
	view, code, err := fetchRegistration(ctx, m, proposalID)
	require.NoErrorf(t, err, "%s did not answer for proposal %s:%s", m.Name, proposalID, Diagnostics(m))
	return view, code
}

// ListRegistrations returns one machine's view of every proposal in the site.
func ListRegistrations(ctx context.Context, t *testing.T, m *Machine) []Registration {
	t.Helper()
	registrations, code, err := getJSON[[]Registration](ctx, m.URL+"/registrations")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, code)
	return registrations
}

// ListConflicts returns one machine's view of every resolved duplicate claim.
func ListConflicts(ctx context.Context, t *testing.T, m *Machine) []Conflict {
	t.Helper()
	conflicts, code, err := getJSON[[]Conflict](ctx, m.URL+"/registrations/conflicts")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, code)
	return conflicts
}

// WaitForAPI blocks until a machine's registration API answers.
//
// That is the scenario's readiness signal, and it is a real one: the platform
// does not listen until its Event Fabric has connected, its projection has
// replayed the retained journal, and its handlers have worked through what was
// waiting for them. An answer on this endpoint means all of that already
// happened.
func WaitForAPI(ctx context.Context, t *testing.T, m *Machine) {
	t.Helper()
	cond := func() bool {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL+"/registrations", http.NoBody)
		if err != nil {
			return false
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return false
		}
		defer func() { _ = response.Body.Close() }()
		return response.StatusCode == http.StatusOK
	}
	abort := func() (bool, string) {
		if m.Exited() {
			return true, fmt.Sprintf("%s exited before serving its registration API:\n%s", m.Name, m.Output())
		}
		return false, ""
	}
	diag := DiagStringer(func() string {
		return fmt.Sprintf("%s never served its registration API:\n%s", m.Name, m.Output())
	})
	WaitFor(t, m.Name+" serving registration API", APIWaitTimeout, apiPollInterval, cond, abort, diag)
}

// WaitForRegistrationStatus polls one machine until a proposal reaches want,
// which is how a real client learns its registration was decided.
func WaitForRegistrationStatus(ctx context.Context, t *testing.T, m *Machine, proposalID, want string) Registration {
	t.Helper()
	return WaitForRegistration(ctx, t, m, proposalID,
		func(view Registration) bool { return view.Status == want },
		"status "+want)
}

// WaitForRegistration polls one machine until a proposal's projected view
// satisfies reached. Polling is the client's only confirmation mechanism:
// nothing is pushed, and the platform makes no promise about when an expected
// machine answers.
//
// Waiting on a condition rather than on the overall status matters. A proposal is
// pending from the moment it is taken until the site decides it, so "wait for
// pending" is not waiting at all; what a scenario usually means is that some
// machine has answered, and that shows up per instance.
func WaitForRegistration(ctx context.Context, t *testing.T, m *Machine, proposalID string, reached func(Registration) bool, what string, extra ...fmt.Stringer) Registration {
	t.Helper()
	var poll LastPoll
	cond := func() bool {
		view, code, err := fetchRegistration(ctx, m, proposalID)
		poll.Record(view, code, err)
		return err == nil && code == http.StatusOK && reached(view)
	}
	abort := func() (bool, string) {
		if m.Exited() {
			return true, fmt.Sprintf("%s exited while waiting for proposal %s to reach %s:\n%s", m.Name, proposalID, what, m.Output())
		}
		return false, ""
	}
	diag := DiagStringer(func() string {
		return fmt.Sprintf("%s never reported proposal %s reaching %s; %s%s",
			m.Name, proposalID, what, &poll, appended(extra))
	})
	WaitFor(t, fmt.Sprintf("proposal %s reaching %s on %s", proposalID, what, m.Name), APIWaitTimeout, apiPollInterval, cond, abort, diag)
	return poll.registration()
}

// LastPoll is what the most recently completed poll saw.
//
// It is mutex-guarded because a poll condition and the failure message that reads
// its fields can run on different goroutines. The previous shape passed &code into
// the message instead, which printed the pointer's address rather than the status
// ("last code 49112166178816").
type LastPoll struct {
	mu   sync.Mutex
	code int
	err  error
	view Registration
	seen bool
}

// Record stores what a completed poll saw.
func (l *LastPoll) Record(view Registration, code int, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.view, l.code, l.err, l.seen = view, code, err, true
}

func (l *LastPoll) registration() Registration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.view
}

// String says what the last poll actually got. The distinction worth preserving
// is between a machine that answered with a state the wait did not want and one
// that did not answer at all: the second is not a slow decision, it is a machine
// that stopped serving.
func (l *LastPoll) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case !l.seen:
		return "no poll ever completed"
	case l.err != nil:
		return fmt.Sprintf("last request failed: %v", l.err)
	case l.code == http.StatusOK:
		return fmt.Sprintf("last response HTTP %d, view %+v", l.code, l.view)
	default:
		return fmt.Sprintf("last response HTTP %d", l.code)
	}
}

// ConfirmedBy reports whether one expected machine has accepted a proposal.
func ConfirmedBy(machine string) func(Registration) bool {
	return func(view Registration) bool {
		for _, instance := range view.PlatformInstances {
			if instance.Machine == machine {
				return instance.Status == "accepted"
			}
		}
		return false
	}
}
