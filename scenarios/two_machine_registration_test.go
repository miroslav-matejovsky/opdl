package scenarios

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTwoMachineRegistration is the registration use case as a customer meets
// it: two real platform processes, built from one blueprint, talking over a real
// fabric, driven only through REST and their event logs.
//
// It is the scenario the two-phase design exists for. A request taken by one
// machine is not registered until every machine the deployment declares has
// accepted it, so the interesting part is what the platform does while one of
// them is not running: it waits, it says it is waiting, and it says which
// machine it is waiting for.
func TestTwoMachineRegistration(t *testing.T) {
	ctx := t.Context()
	scenariosDir, err := filepath.Abs(".")
	require.NoError(t, err)
	outDir := t.TempDir()
	buildProject(ctx, t, filepath.Join(scenariosDir, "testdata"), outDir, "two-machine")

	nodeA := eventNode{
		Project: "two-machine", Environment: "development",
		Site: "local", Machine: "node-a", Role: "all-in-one",
	}
	nodeB := eventNode{
		Project: "two-machine", Environment: "development",
		Site: "local", Machine: "node-b", Role: "all-in-one",
	}
	const (
		unitType = 7
		unitID   = 42
	)
	request := `{"unit_type":7,"unit_id":42,"unit_type_name_advertised":"Billing","role":"Master"}`

	// Start node-a alone. node-b is in its topology and is not running.
	first := startMachine(ctx, t, machineBinary(outDir, "two-machine", "node-a"), outDir, "node-a")
	waitForAPI(ctx, t, first)

	require.Equal(t, http.StatusAccepted, postRegistration(ctx, t, first, request),
		"202 means the request was taken, not that the unit is registered")

	// node-a answers for itself as soon as it looks at the request. That is one
	// expected instance of two, so it is not acceptance.
	waitForEventTypes(t, first.eventsDir, nodeA,
		"platform.registration.requested",
		"platform.registration.confirmed")

	pending, code := getRegistration(ctx, t, first, unitType, unitID)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "pending", pending.Status,
		"the site cannot accept a request node-b has not seen, however long node-a waits")
	require.Equal(t, "accepted", pending.instance(t, "node-a").Status)
	require.Equal(t, "pending", pending.instance(t, "node-b").Status,
		"node-b is expected and offline, which is visible rather than silent")
	require.Equal(t, "node-a", pending.Machine)
	require.Equal(t, "127.0.0.1", pending.IP)

	listed := listRegistrations(ctx, t, first)
	require.Equal(t, []registration{pending}, listed,
		"a pending request is listed, with the same state the status endpoint reports")

	// node-b was never asked to accept anything, so nothing accepted it.
	require.NotContains(t, eventTypes(readEvents(t, first.eventsDir, nodeA)), "platform.registration.accepted")

	// Start the machine the site was waiting for. Nothing tells it about the
	// request: it finds it on the fabric and answers on its own schedule.
	second := startMachine(ctx, t, machineBinary(outDir, "two-machine", "node-b"), outDir, "node-b")
	waitForAPI(ctx, t, second)

	// The client polls where it asked, which is its only confirmation mechanism.
	accepted := waitForRegistrationStatus(ctx, t, first, unitType, unitID, "accepted")
	require.Equal(t, "accepted", accepted.instance(t, "node-a").Status)
	require.Equal(t, "accepted", accepted.instance(t, "node-b").Status)
	require.Equal(t, "node-a", accepted.Machine, "the origin is where the client asked, and does not move")
	require.Equal(t, "127.0.0.1", accepted.IP)
	require.Equal(t, "Billing", accepted.UnitTypeNameAdvertised)
	require.Equal(t, "Master", *accepted.Role)

	// Both machines list the accepted registration identically: the list is the
	// site's state, and both machines carry it.
	require.Equal(t, []registration{accepted}, listRegistrations(ctx, t, first))
	require.Equal(t, []registration{accepted}, listRegistrations(ctx, t, second))

	// node-b holds the same registration and still answers 404 for its status:
	// it is not the machine the client asked.
	_, code = getRegistration(ctx, t, second, unitType, unitID)
	require.Equal(t, http.StatusNotFound, code,
		"the status endpoint is the origin's, even where the state is everywhere")

	// node-b stated its own confirmation, and nothing else: the origin owns the
	// request's phases.
	require.Equal(t, []string{"platform.registration.confirmed"},
		registrationEvents(readEvents(t, second.eventsDir, nodeB)))

	// An exact retry is the same claim, answered the same way, changing nothing.
	before := registrationEvents(readEvents(t, first.eventsDir, nodeA))
	require.Equal(t, http.StatusAccepted, postRegistration(ctx, t, first, request))
	require.Equal(t, before, registrationEvents(readEvents(t, first.eventsDir, nodeA)),
		"an exact retry must not duplicate a phase event")
	requireSingleEvent(t, readEvents(t, first.eventsDir, nodeA), "platform.registration.requested")
	requireSingleEvent(t, readEvents(t, first.eventsDir, nodeA), "platform.registration.accepted")

	// The same key with a different advertised name is a different claim, from
	// the very machine that holds the key.
	conflicting := `{"unit_type":7,"unit_id":42,"unit_type_name_advertised":"Payments","role":"Master"}`
	require.Equal(t, http.StatusConflict, postRegistration(ctx, t, first, conflicting))
	onOrigin := requireSingleEvent(t,
		waitForEventTypes(t, first.eventsDir, nodeA, "platform.registration.conflict"),
		"platform.registration.conflict")
	require.Contains(t, onOrigin.Tags, "warning", "a refused claim is an operational anomaly")
	require.Equal(t, "registration_key_conflict", onOrigin.payload(t)["reason"])

	// The same key from the other machine is a different claim too: the key is
	// the site's, not a machine's.
	require.Equal(t, http.StatusConflict, postRegistration(ctx, t, second, request))
	onOther := requireSingleEvent(t,
		waitForEventTypes(t, second.eventsDir, nodeB, "platform.registration.conflict"),
		"platform.registration.conflict")
	require.Contains(t, onOther.Tags, "warning")
	require.Equal(t, "node-a", onOther.payload(t)["existing"].(map[string]any)["machine"],
		"the conflict names both sides, so a reader sees the difference without asking the platform")
	require.Equal(t, "node-b", onOther.payload(t)["attempted"].(map[string]any)["machine"])

	// Every refused claim left the registration exactly as it was.
	after, code := getRegistration(ctx, t, first, unitType, unitID)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, accepted, after, "a refused claim preserves the accepted registration")
	require.Equal(t, []registration{accepted}, listRegistrations(ctx, t, second))

	// Each machine's log is its own, separated by the identity it was built
	// with rather than by anything observed at runtime.
	requireOnlyNodeInLog(t, first.eventsDir, nodeA)
	requireOnlyNodeInLog(t, second.eventsDir, nodeB)

	// The phases the origin reported, in the order they happened.
	requireEventOrder(t, readEvents(t, first.eventsDir, nodeA),
		"platform.registration.requested",
		"platform.registration.confirmed",
		"platform.registration.accepted",
		"platform.registration.conflict",
	)
}
