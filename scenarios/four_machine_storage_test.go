package scenarios

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFourMachineStorageTopologyAndFailure is the proof the three-storage-node
// topology exists for.
//
// A site of three or more machines runs the site journal on three of them,
// replicated three ways, and every other machine is a client of those three.
// Three rather than two is what lets the journal's metadata group keep a quorum
// when one member is lost, and it is the only reason the blueprint has a cluster
// port at all.
//
// Four machines rather than three is deliberate: it proves both halves of the
// rule at once, that exactly three machines are selected and that the fourth is
// a client that binds nothing.
//
// The scenario asserts the resolved topology, then removes a storage machine and
// requires the site to keep accepting and projecting events, then brings it back
// onto its own journal storage and requires it to rejoin with the same state.
func TestFourMachineStorageTopologyAndFailure(t *testing.T) {
	ctx := t.Context()
	outDir := t.TempDir()
	deployment := deploySite(ctx, t, outDir, t.TempDir(), "four-machine")

	// Storage is selected by sorted machine name, so node-a, node-b, and node-c
	// store the journal and node-d does not. Nothing tells them that: each derives
	// it from the site membership in its own descriptor.
	storage := storageMachines(machineNames("four-machine"))
	require.Equal(t, []string{"node-a", "node-b", "node-c"}, storage)

	deployment.startTogether(ctx, t, "node-a", "node-b", "node-c", "node-d")
	nodeA := deployment.machine(t, "node-a")
	nodeC := deployment.machine(t, "node-c")
	nodeD := deployment.machine(t, "node-d")

	// The three selected machines each own a server, replicate three ways, and
	// route to exactly the other two. The routes are what the cluster port is for,
	// and only these machines have any.
	for _, name := range storage {
		m := deployment.machine(t, name)
		fabric := lastFabricConfig(t, m)
		require.Truef(t, fabric.storage, "%s must store the site journal", name)
		require.Truef(t, fabric.binds, "%s must bind its NATS listener", name)
		require.NotEqualf(t, "none", fabric.cluster, "%s must bind a cluster listener", name)
		require.NotEqualf(t, "none", fabric.routes, "%s must route to the other storage machines", name)
		requireReported(t, m, "replicas=3", "a site of three or more machines replicates three ways")
	}

	// The fourth machine reaches the same journal and owns none of it. This is the
	// half of the rule that a three-machine site could not prove.
	clientOnly := lastFabricConfig(t, nodeD)
	require.False(t, clientOnly.storage, "node-d must not store the site journal")
	require.False(t, clientOnly.binds, "node-d must bind no NATS listener")
	require.Equal(t, "none", clientOnly.cluster, "node-d must bind no cluster listener")
	require.Equal(t, "none", clientOnly.routes, "node-d has no cluster to route to")
	require.NotContains(t, clientOnly.servers, clientOnly.endpoint,
		"node-d runs no server, so its own address is not one it connects to")

	// Every machine of the site folded the same journal, so they are one site
	// rather than four that happen to be running.
	require.Equal(t, journalOf(t, nodeA), journalOf(t, nodeD))

	// Published through the client-only machine, replayed through a storage one.
	fromD := propose(ctx, t, nodeD, `{"unit_type":5,"unit_id":81,"unit_type_name_advertised":"Client published"}`)
	onA := waitForRegistrationStatus(ctx, t, nodeA, fromD.ProposalID, "accepted")
	require.Equal(t, "node-d", onA.Machine)

	// Losing one of the three storage machines leaves two, which is still a quorum
	// of the metadata group. The site must keep accepting and projecting events,
	// which is the entire claim the three-replica topology makes.
	nodeC.stop()

	afterLoss := proposeEventually(ctx, t, nodeA,
		`{"unit_type":5,"unit_id":82,"unit_type_name_advertised":"After storage loss"}`)

	// The surviving storage machines write their own confirmations into the
	// journal and project each other's. That is the proof the journal still
	// accepts writes and still orders them with one storage machine gone, which
	// is what the three-replica topology claims.
	//
	// Only the surviving storage machines are required here. Whether the
	// client-only machine keeps projecting across the loss depends on which
	// storage node its client happened to be connected to, and that is a separate
	// unproven property recorded in docs/backlog/event-fabric.md rather than
	// asserted as though it held.
	for _, confirmer := range []string{"node-a", "node-b"} {
		require.Truef(t, nodeA.running(), "node-a stopped serving after node-c was killed:%s",
			diagnose(deployment.machines))
		waitForRegistration(ctx, t, nodeA, afterLoss.ProposalID, confirmedBy(confirmer),
			"confirmation from "+confirmer+diagnose(deployment.machines))
	}

	// The proposal itself stays pending, and that is the registration contract
	// rather than a fabric failure: a registration needs every machine of the
	// site's static membership to confirm, not merely the reachable ones. The
	// absent machine is exactly what it is still waiting for.
	pending, _ := getRegistration(ctx, t, nodeA, afterLoss.ProposalID)
	require.Equal(t, "pending", pending.Status,
		"a registration is not decided until every machine of the site has confirmed")

	// Bringing it back on its own journal storage is what makes it the same node
	// returning rather than a new one joining. It must rejoin the cluster, catch
	// up on everything published while it was gone, and then confirm, which is
	// what finally decides the proposal.
	nodeC.restart(ctx, t)
	waitForAPI(ctx, t, nodeC)

	decided := waitForRegistrationStatus(ctx, t, nodeA, afterLoss.ProposalID, "accepted")
	require.Len(t, decided.PlatformInstances, 4,
		"the site decided with all four machines confirming")

	// Every machine of the whole site answers with the same state, so the loss and
	// the rejoin produced one view rather than a divergent one. Including the
	// client-only machine here is what proves it reconverges once the site is
	// whole, whatever it did during the outage.
	expected := listRegistrations(ctx, t, nodeA)
	require.Len(t, expected, 2)
	for _, m := range []*machine{nodeC, nodeD} {
		waitForRegistrationStatus(ctx, t, m, afterLoss.ProposalID, "accepted")
		require.Equalf(t, expected, listRegistrations(ctx, t, m),
			"%s did not return the site's projected state", m.name)
	}
}

// proposeEventually submits a registration until the site takes it.
//
// It exists for the window just after a storage machine is lost. The journal's
// replica group has to elect a new leader before it can accept a write again,
// and until it does the platform answers the client with a failure rather than
// holding the request. The window is short, but it is real: losing a storage
// machine is not transparent to a writer, and a client that submits once during
// it gets an error back.
//
// Retrying here is what a client does, not a sleep hiding a defect. The bound
// stays the scenario's ordinary one, so a site that never recovers still fails.
// If the platform later retries internally, this helper collapses back into
// propose and the change is visible in this comment.
func proposeEventually(ctx context.Context, t *testing.T, m *machine, body string) proposalAccepted {
	t.Helper()
	var accepted proposalAccepted
	var lastCode int
	require.Eventually(t, func() bool {
		result, code := postRegistration(ctx, t, m, body)
		lastCode = code
		if code != http.StatusAccepted {
			return false
		}
		accepted = result
		return true
	}, apiWaitTimeout, apiPollInterval,
		"%s never took the proposal after a storage machine was lost; last code %d:\n%s",
		m.name, &lastCode, m.output)
	require.NotEmpty(t, accepted.ProposalID)
	return accepted
}

// lastFabricConfig returns the effective Event Fabric configuration a running
// machine reported most recently, without stopping it to read it.
func lastFabricConfig(t *testing.T, m *machine) fabricConfig {
	t.Helper()
	configs := parseFabricConfigs(t, m.name, m.output.String())
	return configs[len(configs)-1]
}
