package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// This file is the scenario side of the platform's registration API: enough to
// call it over HTTP and read what came back.
//
// It is a black-box view, like the event log's. It re-declares the JSON shapes
// it needs rather than importing the platform's Go types, so a scenario checks
// the published contract and a change to it shows up here as a failing scenario
// rather than as a silently recompiled struct.

const (
	// apiPollInterval is how often a wait retries a request.
	apiPollInterval = 100 * time.Millisecond
	// apiWaitTimeout bounds a wait for the API to answer or to reach a state.
	// It is generous: it covers a machine starting, a fabric forming, and every
	// expected machine reconciling on its own schedule.
	apiWaitTimeout = 30 * time.Second
)

// registration is the observable shape of one registration in the API's JSON.
type registration struct {
	// UnitType is the registered unit type identifier.
	UnitType uint8 `json:"unit_type"`
	// UnitID is the registered unit identifier.
	UnitID uint16 `json:"unit_id"`
	// UnitTypeNameAdvertised is the unit's advertised type name.
	UnitTypeNameAdvertised string `json:"unit_type_name_advertised"`
	// Role is the optional advertised role.
	Role *string `json:"role,omitempty"`
	// Machine is the descriptor machine the request originated on.
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

// statusPath is the status endpoint of one unit key.
func statusPath(unitType, unitID int) string {
	return fmt.Sprintf("/registrations/%d/%d/status", unitType, unitID)
}

// registrationEvents returns the registration event types in a machine's log, in
// order. A machine states its fabric's lifecycle into the same log, and an
// assertion about what a registration reported should not have to know that.
func registrationEvents(records []eventRecord) []string {
	types := make([]string, 0, len(records))
	for _, record := range records {
		if record.Source == "registration" {
			types = append(types, record.Type)
		}
	}
	return types
}

// postRegistration submits a registration request to one machine and returns the
// status code it answered with.
func postRegistration(ctx context.Context, t *testing.T, m *machine, body string) int {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, m.url+"/registrations", bytes.NewReader([]byte(body)))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	return response.StatusCode
}

// getRegistration returns one machine's status response for a unit key, and the
// status code it answered with. The registration is meaningful only on 200.
func getRegistration(ctx context.Context, t *testing.T, m *machine, unitType, unitID int) (view registration, code int) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, m.url+statusPath(unitType, unitID), http.NoBody)
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

// listRegistrations returns one machine's view of every registration request in
// the site.
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

// waitForAPI blocks until a machine's registration API answers, which is the
// scenario's signal that the process has finished starting.
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

// waitForRegistrationStatus polls one machine's status endpoint until the
// request reaches want, which is how a real client learns its registration was
// accepted. Polling is the client's only confirmation mechanism: nothing is
// pushed, and the platform makes no promise about when an expected machine
// answers.
func waitForRegistrationStatus(ctx context.Context, t *testing.T, m *machine, unitType, unitID int, want string) registration {
	t.Helper()
	var last registration
	require.Eventually(t, func() bool {
		view, code := getRegistration(ctx, t, m, unitType, unitID)
		if code != http.StatusOK {
			return false
		}
		last = view
		return view.Status == want
	}, apiWaitTimeout, apiPollInterval,
		"%s never reported %d/%d as %s; last saw %+v", m.name, unitType, unitID, want, &last)
	return last
}
