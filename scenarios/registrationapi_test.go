package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
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

// postRegistration submits a registration request to one machine and returns
// what it answered with. The proposal is meaningful only on 202.
func postRegistration(ctx context.Context, t *testing.T, m *machine, body string) (accepted proposalAccepted, code int) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, m.url+"/registrations", bytes.NewReader([]byte(body)))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusAccepted {
		return proposalAccepted{}, response.StatusCode
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&accepted))
	return accepted, response.StatusCode
}

// propose submits a registration request and requires the journal to take it,
// which is what a scenario means when it says a client registered something.
func propose(ctx context.Context, t *testing.T, m *machine, body string) proposalAccepted {
	t.Helper()
	accepted, code := postRegistration(ctx, t, m, body)
	require.Equal(t, http.StatusAccepted, code, "%s did not take the proposal:\n%s", m.name, m.output)
	require.NotEmpty(t, accepted.ProposalID, "202 hands back the handle the client polls with")
	return accepted
}

// getRegistration returns one machine's view of a proposal, and the status code
// it answered with. The registration is meaningful only on 200.
func getRegistration(ctx context.Context, t *testing.T, m *machine, proposalID string) (view registration, code int) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, m.url+"/registrations/"+proposalID, http.NoBody)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return registration{}, response.StatusCode
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&view))
	return view, response.StatusCode
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
func waitForRegistration(ctx context.Context, t *testing.T, m *machine, proposalID string, reached func(registration) bool, what string) registration {
	t.Helper()
	var last registration
	var lastCode int
	require.Eventually(t, func() bool {
		view, code := getRegistration(ctx, t, m, proposalID)
		lastCode = code
		if code != http.StatusOK {
			return false
		}
		last = view
		return reached(view)
	}, apiWaitTimeout, apiPollInterval,
		"%s never reported proposal %s reaching %s; last code %d, last view %+v",
		m.name, proposalID, what, &lastCode, &last)
	return last
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
