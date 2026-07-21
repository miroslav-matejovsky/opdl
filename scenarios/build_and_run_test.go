package scenarios

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBuildAndRunSingleMachine is the bare-minimum end-to-end scenario: drive
// the builder CLI to build the single machine in the "scenario" example
// blueprint, then run the resulting platform binary and check it starts its
// registration API, decides a proposal, and reports its configuration. Both are
// external processes; nothing here imports builder or platform Go code.
func TestBuildAndRunSingleMachine(t *testing.T) {
	ctx := t.Context()
	outDir := filepath.Join(scenarioDir(t), "out")
	deployment := deploySite(ctx, t, outDir, filepath.Join(scenarioDir(t), "work"), "scenario")
	node := deployment.machine(t, "node")
	require.Nil(t, readManifest(t, node.binaryPath).Standby,
		"an explicit per-machine opt-out must package only the primary launch")
	node.start(ctx, t)
	waitForAPI(ctx, t, node)

	require.Empty(t, listRegistrations(ctx, t, node),
		"a site that has registered nothing lists nothing")

	// 202 is the whole answer the client gets: the journal took the proposal, and
	// this is the handle to ask about it with. Nothing here says it is registered.
	accepted := propose(ctx, t, node,
		`{"unit_type":7,"unit_id":42,"unit_type_name_advertised":"Scenario service","role":"Master"}`)
	require.Positive(t, accepted.Sequence, "the proposal has a place in the site's history")

	// A one-machine site is its own only expected machine, so it decides on its
	// own, though still on its own schedule.
	status := waitForRegistrationStatus(ctx, t, node, accepted.ProposalID, "accepted")
	require.Equal(t, accepted.ProposalID, status.ProposalID)
	require.Equal(t, "node", status.Machine)
	require.Equal(t, "127.0.0.1", status.IP)
	require.Equal(t, "Scenario service", status.UnitTypeNameAdvertised)
	require.Equal(t, "Master", *status.Role)
	require.Len(t, status.PlatformInstances, 1)
	require.Equal(t, "node", status.PlatformInstances[0].Machine)
	require.Equal(t, "127.0.0.1", status.PlatformInstances[0].IP)
	require.Equal(t, "accepted", status.PlatformInstances[0].Status)

	require.Equal(t, []registration{status}, listRegistrations(ctx, t, node),
		"the list and the status endpoint are the same projection")
	require.Empty(t, listConflicts(ctx, t, node), "one claim is not a conflict")

	// An exact retry is the same claim, so it is the same proposal: the identity
	// is derived from the request, not from when it arrived.
	retry := propose(ctx, t, node,
		`{"unit_type":7,"unit_id":42,"unit_type_name_advertised":"Scenario service","role":"Master"}`)
	require.Equal(t, accepted.ProposalID, retry.ProposalID, "an exact retry is the same claim")

	after, code := getRegistration(ctx, t, node, accepted.ProposalID)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, status, after, "a retry changed nothing")
	require.Len(t, listRegistrations(ctx, t, node), 1, "a retry is not a second registration")

	// A proposal nobody made is not found.
	_, code = getRegistration(ctx, t, node, "0000000000000000000000000000000000000000000000000000000000000000")
	require.Equal(t, http.StatusNotFound, code)

	// The machine reported the configuration it booted with, including where its
	// journal lives and that it is a site of one.
	logs := node.logs()
	require.Contains(t, logs, "platform configuration")
	require.Contains(t, logs, "one-member site", "a standalone deployment is a site of one")
	require.Contains(t, logs, "data_dir="+filepath.ToSlash(node.sockets.dataDir))
	require.Contains(t, logs, "credentials_file=(none: loopback only)",
		"a loopback deployment may run unauthenticated, and says so")
	require.NotContains(t, logs, "events_dir", "local event files are not a runtime path any more")
}
