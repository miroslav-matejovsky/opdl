package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
	// apiWaitTimeout bounds a wait for the API to answer or to reach a state.
	// It is generous: it covers a machine starting its NATS server, replaying the
	// site journal, and every expected machine deciding on its own schedule.
	apiWaitTimeout = 60 * time.Second
)

// proposalAccepted is the observable shape of a 202: the journal took the
// proposal, and this is the handle to poll.
type proposalAccepted struct {
	// ProposalID is the proposal's stable identity and its status key.
	ProposalID string `json:"proposal_id"`
	// Sequence is the proposal's position in the site journal.
	Sequence uint64 `json:"sequence"`
}

// registration is the observable shape of one registration in the API's JSON.
type registration struct {
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
	PlatformInstances []platformInstance `json:"platform_instances"`
}

// platformInstance is one platform instance's progress for a registration.
type platformInstance struct {
	// Machine is the platform instance's descriptor machine.
	Machine string `json:"machine"`
	// IP is the platform instance's descriptor IP.
	IP string `json:"ip"`
	// Status is pending, accepted, or rejected.
	Status string `json:"status"`
	// Reason is the optional bounded rejection code.
	Reason *string `json:"reason,omitempty"`
}

// conflict is the observable shape of one resolved duplicate claim.
type conflict struct {
	// UnitType is the unit type identifier shared by the competing proposals.
	UnitType uint8 `json:"unit_type"`
	// UnitID is the unit identifier shared by the competing proposals.
	UnitID uint16 `json:"unit_id"`
	// ResolutionStatus is resolved once the winner is determined.
	ResolutionStatus string `json:"resolution_status"`
	// Winner is the proposal that keeps the unit key.
	Winner registration `json:"winner"`
	// Losers are the proposals rejected for the key.
	Losers []registration `json:"losers"`
}

// instance returns one expected platform instance's entry, failing when the
// projection does not cover that machine at all.
func (r registration) instance(t *testing.T, machine string) platformInstance {
	t.Helper()
	for _, entry := range r.PlatformInstances {
		if entry.Machine == machine {
			return entry
		}
	}
	require.FailNow(t, "no platform instance entry", "machine %s not in %+v", machine, r.PlatformInstances)
	return platformInstance{}
}

// submitRegistration submits a registration request to one machine and reports
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
func submitRegistration(ctx context.Context, m *machine, body string) (proposalAccepted, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, m.url+"/registrations", bytes.NewReader([]byte(body)))
	if err != nil {
		return proposalAccepted{}, 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return proposalAccepted{}, 0, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusAccepted {
		return proposalAccepted{}, response.StatusCode, nil
	}
	var accepted proposalAccepted
	if err := json.NewDecoder(response.Body).Decode(&accepted); err != nil {
		return proposalAccepted{}, response.StatusCode, err
	}
	return accepted, response.StatusCode, nil
}

// fetchRegistration returns one machine's view of a proposal, the status code it
// answered with, and any transport failure. The registration is meaningful only
// on 200. It reports rather than asserts for the reason submitRegistration does.
func fetchRegistration(ctx context.Context, m *machine, proposalID string) (registration, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, m.url+"/registrations/"+proposalID, http.NoBody)
	if err != nil {
		return registration{}, 0, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return registration{}, 0, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return registration{}, response.StatusCode, nil
	}
	var view registration
	if err := json.NewDecoder(response.Body).Decode(&view); err != nil {
		return registration{}, response.StatusCode, err
	}
	return view, response.StatusCode, nil
}

// propose submits a registration request and requires the journal to take it,
// which is what a scenario means when it says a client registered something.
func propose(ctx context.Context, t *testing.T, m *machine, body string) proposalAccepted {
	t.Helper()
	accepted, code, err := submitRegistration(ctx, m, body)
	require.NoErrorf(t, err, "%s did not answer the proposal:%s", m.name, diagnostics(m))
	require.Equalf(t, http.StatusAccepted, code, "%s did not take the proposal:%s", m.name, diagnostics(m))
	require.NotEmpty(t, accepted.ProposalID, "202 hands back the handle the client polls with")
	return accepted
}

// getRegistration returns one machine's view of a proposal, and the status code
// it answered with. A machine that does not answer at all is a failure here, so
// this is for the test goroutine; a poll wants fetchRegistration.
func getRegistration(ctx context.Context, t *testing.T, m *machine, proposalID string) (view registration, code int) {
	t.Helper()
	view, code, err := fetchRegistration(ctx, m, proposalID)
	require.NoErrorf(t, err, "%s did not answer for proposal %s:%s", m.name, proposalID, diagnostics(m))
	return view, code
}

// listRegistrations returns one machine's view of every proposal in the site.
func listRegistrations(ctx context.Context, t *testing.T, m *machine) []registration {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, m.url+"/registrations", http.NoBody)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusOK, response.StatusCode)

	var registrations []registration
	require.NoError(t, json.NewDecoder(response.Body).Decode(&registrations))
	return registrations
}

// listConflicts returns one machine's view of every resolved duplicate claim.
func listConflicts(ctx context.Context, t *testing.T, m *machine) []conflict {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, m.url+"/registrations/conflicts", http.NoBody)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusOK, response.StatusCode)

	var conflicts []conflict
	require.NoError(t, json.NewDecoder(response.Body).Decode(&conflicts))
	return conflicts
}

// waitForAPI blocks until a machine's registration API answers.
//
// That is the scenario's readiness signal, and it is a real one: the platform
// does not listen until its Event Fabric has connected, its projection has
// replayed the retained journal, and its handlers have worked through what was
// waiting for them. An answer on this endpoint means all of that already
// happened.
func waitForAPI(ctx context.Context, t *testing.T, m *machine) {
	t.Helper()
	require.Eventually(t, func() bool {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, m.url+"/registrations", http.NoBody)
		if err != nil {
			return false
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return false
		}
		defer func() { _ = response.Body.Close() }()
		return response.StatusCode == http.StatusOK
	}, apiWaitTimeout, apiPollInterval, "%s never served its registration API:\n%s", m.name, m.output)
}

// waitForRegistrationStatus polls one machine until a proposal reaches want,
// which is how a real client learns its registration was decided.
func waitForRegistrationStatus(ctx context.Context, t *testing.T, m *machine, proposalID, want string) registration {
	t.Helper()
	return waitForRegistration(ctx, t, m, proposalID,
		func(view registration) bool { return view.Status == want },
		"status "+want)
}

// waitForRegistration polls one machine until a proposal's projected view
// satisfies reached. Polling is the client's only confirmation mechanism:
// nothing is pushed, and the platform makes no promise about when an expected
// machine answers.
//
// Waiting on a condition rather than on the overall status matters. A proposal is
// pending from the moment it is taken until the site decides it, so "wait for
// pending" is not waiting at all; what a scenario usually means is that some
// machine has answered, and that shows up per instance.
func waitForRegistration(ctx context.Context, t *testing.T, m *machine, proposalID string, reached func(registration) bool, what string, extra ...fmt.Stringer) registration {
	t.Helper()
	var poll lastPoll
	require.Eventually(t, func() bool {
		view, code, err := fetchRegistration(ctx, m, proposalID)
		poll.record(view, code, err)
		return err == nil && code == http.StatusOK && reached(view)
	}, apiWaitTimeout, apiPollInterval,
		"%s never reported proposal %s reaching %s; %s%s",
		m.name, proposalID, what, &poll, appended(extra))
	return poll.registration()
}

// lastPoll is what the most recently completed poll saw.
//
// It is mutex-guarded because require.Eventually runs its condition on its own
// goroutine: the poll writes these fields and the failure message reads them.
// The previous shape passed &code into the message instead, which printed the
// pointer's address rather than the status ("last code 49112166178816").
type lastPoll struct {
	mu   sync.Mutex
	code int
	err  error
	view registration
	seen bool
}

func (l *lastPoll) record(view registration, code int, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.view, l.code, l.err, l.seen = view, code, err, true
}

func (l *lastPoll) registration() registration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.view
}

// String says what the last poll actually got. The distinction worth preserving
// is between a machine that answered with a state the wait did not want and one
// that did not answer at all: the second is not a slow decision, it is a machine
// that stopped serving.
func (l *lastPoll) String() string {
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

// confirmedBy reports whether one expected machine has accepted a proposal.
func confirmedBy(machine string) func(registration) bool {
	return func(view registration) bool {
		for _, instance := range view.PlatformInstances {
			if instance.Machine == machine {
				return instance.Status == "accepted"
			}
		}
		return false
	}
}
