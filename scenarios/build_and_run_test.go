package scenarios

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestBuildAndRunSingleMachine is the bare-minimum end-to-end scenario: drive
// the builder CLI to build the single machine in the "scenario" example
// blueprint, then run the resulting platform binary and check it starts its
// registration API, persists a request, and reports its configuration. Both are external
// processes; nothing here imports builder or platform Go code.
func TestBuildAndRunSingleMachine(t *testing.T) {
	ctx := t.Context()
	scenariosDir, err := filepath.Abs(".")
	require.NoError(t, err)
	outDir := t.TempDir()
	buildProject(ctx, t, filepath.Join(scenariosDir, "testdata"), outDir, "scenario")

	platform := startMachine(ctx, t, machineBinary(outDir, "scenario", "node"), outDir, "node")
	url := platform.url
	eventsDir := platform.eventsDir

	require.Eventually(t, func() bool {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, url+"/registrations", http.NoBody)
		if reqErr != nil {
			return false
		}
		resp, getErr := http.DefaultClient.Do(req)
		if getErr != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return false
		}
		var registrations []registration
		return json.NewDecoder(resp.Body).Decode(&registrations) == nil && len(registrations) == 0
	}, 15*time.Second, 100*time.Millisecond, "platform API never became reachable")

	request := []byte(`{"unit_type":7,"unit_id":42,"unit_type_name_advertised":"Scenario service","role":"Master"}`)
	post, err := http.NewRequestWithContext(ctx, http.MethodPost, url+"/registrations", bytes.NewReader(request))
	require.NoError(t, err)
	post.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(post)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusAccepted, response.StatusCode)

	var status registration
	require.Eventually(t, func() bool {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, url+"/registrations/7/42/status", http.NoBody)
		if reqErr != nil {
			return false
		}
		resp, getErr := http.DefaultClient.Do(req)
		if getErr != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return false
		}
		return json.NewDecoder(resp.Body).Decode(&status) == nil && status.Status == "accepted"
	}, 15*time.Second, 100*time.Millisecond, "registration was not accepted")
	require.Equal(t, "node", status.Machine)
	require.Equal(t, "127.0.0.1", status.IP)
	require.Len(t, status.PlatformInstances, 1)
	require.Equal(t, "node", status.PlatformInstances[0].Machine)
	require.Equal(t, "127.0.0.1", status.PlatformInstances[0].IP)
	require.Equal(t, "accepted", status.PlatformInstances[0].Status)

	list, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/registrations", http.NoBody)
	require.NoError(t, err)
	response, err = http.DefaultClient.Do(list)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var registrations []registration
	require.NoError(t, json.NewDecoder(response.Body).Decode(&registrations))
	require.Equal(t, []registration{status}, registrations)

	// The events are recorded against the machine's own deployment identity: the
	// platform named the log after the descriptor it was built with, so this is
	// the whole node identity, checked from outside the process.
	node := eventNode{
		Project:     "scenario",
		Environment: "development",
		Site:        "local",
		Machine:     "node",
		Role:        "all-in-one",
	}
	requireOnlyNodeInLog(t, eventsDir, node)

	// The registration reported itself through events, in the order the phases
	// happened: the request was taken, this platform instance confirmed it, and
	// only then was it accepted.
	records := waitForEventTypes(t, eventsDir, node,
		"platform.registration.requested",
		"platform.registration.confirmed",
		"platform.registration.accepted",
	)
	requireEventOrder(t, records,
		"platform.registration.requested",
		"platform.registration.confirmed",
		"platform.registration.accepted",
	)

	// The requested event's origin is the machine itself, never the client's
	// claim, and it carries the unit the client asked for.
	requested := requireSingleEvent(t, records, "platform.registration.requested")
	require.Equal(t, "registration", requested.Source)
	require.Equal(t, "node", requested.payload(t)["machine"])
	require.Equal(t, "127.0.0.1", requested.payload(t)["ip"])
	require.Equal(t, float64(7), requested.payload(t)["unit_type"])
	require.Equal(t, float64(42), requested.payload(t)["unit_id"])

	accepted := requireSingleEvent(t, records, "platform.registration.accepted")
	require.Equal(t, "node", accepted.payload(t)["machine"])
	require.Equal(t, "127.0.0.1", accepted.payload(t)["ip"])
	require.Greater(t, accepted.Sequence, requested.Sequence, "events are numbered in occurrence order")

	// An exact retry is answered but changes nothing, so it reports nothing. The
	// platform flushes each event before answering, so the file is complete as
	// soon as the response is: no polling, and no chance of a false pass.
	retry, err := http.NewRequestWithContext(ctx, http.MethodPost, url+"/registrations", bytes.NewReader(request))
	require.NoError(t, err)
	retry.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(retry)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusAccepted, response.StatusCode)

	afterRetry := readEvents(t, eventsDir, node)
	require.Equal(t, eventTypes(records), eventTypes(afterRetry), "an exact retry must not duplicate a phase event")
	requireSingleEvent(t, afterRetry, "platform.registration.requested")
	requireSingleEvent(t, afterRetry, "platform.registration.accepted")

	// Events are read above while the process is live: a force stop is not
	// portable enough to rely on for flushing, and graceful shutdown is covered
	// by the platform's in-process lifecycle tests.
	logs := platform.logs()
	require.Contains(t, logs, "platform configuration")
	require.Contains(t, logs, "events_dir          "+eventsDir)
	require.Contains(t, logs, "one-member site", "a standalone deployment is a fabric of one")
}
