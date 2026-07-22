package scenarios

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/scenarios/internal/harness"
)

// TestBuildAndRunMinimumSite is the bare-minimum end-to-end scenario: build the
// smallest site the platform accepts, then run the resulting platform binaries
// and check they start their registration API, decide a proposal, and report
// their configuration. Nothing here imports platform code.
//
// The minimum is two machines and three platform instances, so "bare minimum" is
// no longer one process. The machines start together because their journal's
// metadata group needs a quorum of its three members before any of them can
// finish starting; node-b's Standby Instance is the third and is not needed for
// that quorum, so it stays down here.
func TestBuildAndRunMinimumSite(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	outDir := filepath.Join(harness.ScenarioDir(t), "out")
	deployment := harness.DeploySite(ctx, t, outDir, filepath.Join(harness.ScenarioDir(t), "work"), "scenario")
	node := deployment.Machine(t, "node-a")
	require.Nil(t, harness.ReadManifest(t, node.BinaryPath).Standby,
		"an explicit per-machine opt-out must package only the primary launch")
	deployment.StartSite(ctx, t)

	require.Empty(t, harness.ListRegistrations(ctx, t, node),
		"a site that has registered nothing lists nothing")

	// 202 is the whole answer the client gets: the journal took the proposal, and
	// this is the handle to ask about it with. Nothing here says it is registered.
	accepted := harness.Propose(ctx, t, node,
		`{"unit_type":7,"unit_id":42,"unit_type_name_advertised":"Scenario service","role":"Master"}`)
	require.Positive(t, accepted.Sequence, "the proposal has a place in the site's history")

	// Registration wants one confirmation per machine, and the site has two, so
	// both must agree before a proposal is accepted.
	status := harness.WaitForRegistrationStatus(ctx, t, node, accepted.ProposalID, "accepted")
	require.Equal(t, accepted.ProposalID, status.ProposalID)
	require.Equal(t, "node-a", status.Machine)
	require.Equal(t, "127.0.0.1", status.IP)
	require.Equal(t, "Scenario service", status.UnitTypeNameAdvertised)
	require.Equal(t, "Master", *status.Role)
	// One confirmation per machine, not per instance: exactly one of a machine's
	// instances is Active and it answers for the machine.
	require.Len(t, status.PlatformInstances, 2)
	for _, instance := range status.PlatformInstances {
		require.Equal(t, "accepted", instance.Status)
	}

	require.Equal(t, []harness.Registration{status}, harness.ListRegistrations(ctx, t, node),
		"the list and the status endpoint are the same projection")
	require.Empty(t, harness.ListConflicts(ctx, t, node), "one claim is not a conflict")

	// An exact retry is the same claim, so it is the same proposal: the identity
	// is derived from the request, not from when it arrived.
	retry := harness.Propose(ctx, t, node,
		`{"unit_type":7,"unit_id":42,"unit_type_name_advertised":"Scenario service","role":"Master"}`)
	require.Equal(t, accepted.ProposalID, retry.ProposalID, "an exact retry is the same claim")

	after, code := harness.GetRegistration(ctx, t, node, accepted.ProposalID)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, status, after, "a retry changed nothing")
	require.Len(t, harness.ListRegistrations(ctx, t, node), 1, "a retry is not a second registration")

	// A proposal nobody made is not found.
	_, code = harness.GetRegistration(ctx, t, node, "0000000000000000000000000000000000000000000000000000000000000000")
	require.Equal(t, http.StatusNotFound, code)

	// The machine reported the configuration it booted with, including where its
	// journal lives and who its site is.
	//
	// Membership is read off the peers line rather than a member count. The count
	// was removed when peers became instances: a machine deploying both
	// contributes two of them, so a number no longer says how many machines a site
	// has, and the line that lists them does.
	logs := node.Output()
	require.Contains(t, logs, "platform configuration")
	require.Contains(t, logs, "peers        node-a/primary (127.0.0.1), node-b/primary (127.0.0.2), node-b/standby (127.0.0.2)",
		"the site's membership is its instances, and a machine with a standby contributes two")
	require.Contains(t, logs, "data_dir     "+filepath.ToSlash(node.Sockets.DataDir))
	require.Contains(t, logs, "credentials_file=(none: loopback only)",
		"a loopback deployment may run unauthenticated, and says so")
	require.NotContains(t, logs, "events_dir", "local event files are not a runtime path any more")
}
