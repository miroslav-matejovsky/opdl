package scenarios

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/scenarios/internal/harness"
	"github.com/miroslav-matejovsky/opdl/scenarios/internal/logscan"
)

// TestTwoMachineEventFabric is the smallest deployment that has to form a real
// Event Fabric: two machines of one site, each built with nothing but its own
// descriptor, sharing one site journal without being told about each other at
// runtime.
//
// It is the scenario the storage topology exists for. Storage is selected per
// platform instance, and this site's first three instances by name are node-a's
// primary and node-b's two, so node-c stores nothing and routes to the others.
// Storage and non-storage machines are therefore not symmetric, and what is
// worth proving is that they behave as if they were: either can publish, and all
// project the same site state from the one journal.
//
// What the site then decides is TestTwoMachineRegistration's subject.
func TestTwoMachineEventFabric(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	outDir := filepath.Join(harness.ScenarioDir(t), "out")
	// node-a and node-b hold the journal between them; node-c is a client of
	// theirs. Nothing told any of them that: all derived it from the same topology
	// in the blueprint the harness rendered and built.
	deployment := harness.DeploySite(ctx, t, outDir, filepath.Join(harness.ScenarioDir(t), "work"), "two-machine")
	nodeA, nodeB := deployment.Machine(t, "node-a"), deployment.Machine(t, "node-c")
	deployment.StartSite(ctx, t)

	// Each machine reports its own part in the site's storage. This is the whole
	// asymmetry, and it is derived rather than configured.
	requireReported(t, nodeA, "storage=true", "node-a's primary is among the first three instances by name")
	requireReported(t, nodeB, "storage=false", "node-c is the fourth, so it routes to the storage instances instead")
	requireReported(t, nodeA, "replicas=3", "a site of three or more instances replicates its journal three ways")
	requireReported(t, nodeB, "replicas=3")

	// Both machines bound themselves to the same journal, named from the
	// deployment scope they share. That is what makes them one site rather than
	// two that happen to be running.
	require.Equal(t, journalOf(t, nodeA), journalOf(t, nodeB),
		"both machines derived the same site journal from the same project, environment, and site")

	// The proof that they share it: a proposal published by the machine with no
	// JetStream storage is projected by both. Nothing routed it there except the
	// topology compiled into the binaries.
	fromB := harness.Propose(ctx, t, nodeB, `{"unit_type":3,"unit_id":9,"unit_type_name_advertised":"Routed"}`)
	onA := harness.WaitForRegistrationStatus(ctx, t, nodeA, fromB.ProposalID, "accepted")
	require.Equal(t, "node-c", onA.Machine,
		"the storage node projected a proposal the other machine published")

	// And the other way, so neither direction is an accident of who stores what.
	fromA := harness.Propose(ctx, t, nodeA, `{"unit_type":3,"unit_id":10,"unit_type_name_advertised":"Stored"}`)
	onB := harness.WaitForRegistrationStatus(ctx, t, nodeB, fromA.ProposalID, "accepted")
	require.Equal(t, "node-a", onB.Machine)

	// Both machines answer with the same site state, because both folded the same
	// ordered journal. Two machines of one site do not disagree about the site.
	require.Equal(t, harness.ListRegistrations(ctx, t, nodeA), harness.ListRegistrations(ctx, t, nodeB))

	// The startup order the platform promises, in three steps rather than two.
	//
	// The listener opens first and stays open for the whole process, so an
	// instance is reachable in every state and a bind failure stops it at startup
	// rather than at a failover. The Event Fabric opens next. Only then does the
	// instance activate, which is when it begins answering domain operations.
	//
	// So listening before the fabric is now correct, and the invariant worth
	// holding is the last step: nothing serves domain operations before the
	// journal behind them is ready. Both machines are still running here; the
	// lines being checked are startup lines, so they are long since written.
	for _, m := range []*harness.Machine{nodeA, nodeB} {
		logs := m.Output()
		listeningAt := strings.Index(logs, "listening on")
		// The line the Event Fabric prints once it is open, which names the journal
		// it reached. Matching on ", journal " rather than on "event fabric" keeps
		// this off the configuration line printed before anything is opened, and
		// off the server name, which is the machine's and differs per machine.
		fabricAt := strings.Index(logs, ", journal ")
		activeAt := strings.Index(logs, "active, serving on")
		require.GreaterOrEqual(t, listeningAt, 0, "%s did not report its API address:\n%s", m.Name, logs)
		require.GreaterOrEqual(t, fabricAt, 0, "%s did not report its event fabric:\n%s", m.Name, logs)
		require.GreaterOrEqual(t, activeAt, 0, "%s never became active:\n%s", m.Name, logs)
		require.Less(t, listeningAt, fabricAt,
			"%s opened its event fabric before its listener; a passive instance must be reachable first:\n%s", m.Name, logs)
		require.Less(t, fabricAt, activeAt,
			"%s served domain operations before its event fabric was ready:\n%s", m.Name, logs)
	}
}

// journalOf reads the site journal name a machine reported at startup. It is
// derived from the deployment scope, so two machines of one site must agree.
func journalOf(t *testing.T, m *harness.Machine) string {
	t.Helper()
	const marker = "journal "
	logs := m.Output()
	tokens := logscan.After(logs, marker)
	require.NotEmpty(t, tokens, "%s did not name its journal:\n%s", m.Name, logs)
	require.NotEmpty(t, tokens[0], "%s named an empty journal:\n%s", m.Name, logs)
	return tokens[0]
}

// requireReported checks a running machine said something at startup, without
// stopping it to find out.
func requireReported(t *testing.T, m *harness.Machine, want string, because ...string) {
	t.Helper()
	require.Containsf(t, m.Output(), want, "%s did not report %q: %s\n%s",
		m.Name, want, strings.Join(because, " "), m.Output())
}
