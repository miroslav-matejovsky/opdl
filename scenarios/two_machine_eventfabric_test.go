package scenarios

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/miroslav-matejovsky/opdl/utils/logscan"
	"github.com/stretchr/testify/require"
)

// TestTwoMachineEventFabric is the smallest deployment that has to form a real
// Event Fabric: two machines of one site, each built with nothing but its own
// descriptor, sharing one site journal without being told about each other at
// runtime.
//
// It is the scenario the storage topology exists for. A site smaller than three
// machines runs JetStream on one deterministic storage node; the other machine
// runs plain NATS and routes to it. The two are therefore not symmetric, and
// what is worth proving is that they behave as if they were: either machine can
// publish, and both project the same site state from the one journal.
//
// What the site then decides is TestTwoMachineRegistration's subject.
func TestTwoMachineEventFabric(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	outDir := filepath.Join(scenarioDir(t), "out")
	// node-a sorts first, so it is the site's storage node and node-b is a client
	// of it. Nothing told either of them that: both derived it from the same
	// topology in the blueprint the harness rendered and built.
	deployment := deploySite(ctx, t, outDir, filepath.Join(scenarioDir(t), "work"), "two-machine")
	nodeA, nodeB := deployment.machine(t, "node-a"), deployment.machine(t, "node-b")
	deployment.startAll(ctx, t)

	// Each machine reports its own part in the site's storage. This is the whole
	// asymmetry, and it is derived rather than configured.
	requireReported(t, nodeA, "storage=true", "node-a sorts first, so it stores the site journal")
	requireReported(t, nodeB, "storage=false", "node-b routes to the storage node instead")
	requireReported(t, nodeA, "replicas=1", "a site smaller than three machines runs one replica")
	requireReported(t, nodeB, "replicas=1")

	// Both machines bound themselves to the same journal, named from the
	// deployment scope they share. That is what makes them one site rather than
	// two that happen to be running.
	require.Equal(t, journalOf(t, nodeA), journalOf(t, nodeB),
		"both machines derived the same site journal from the same project, environment, and site")

	// The proof that they share it: a proposal published by the machine with no
	// JetStream storage is projected by both. Nothing routed it there except the
	// topology compiled into the binaries.
	fromB := propose(ctx, t, nodeB, `{"unit_type":3,"unit_id":9,"unit_type_name_advertised":"Routed"}`)
	onA := waitForRegistrationStatus(ctx, t, nodeA, fromB.ProposalID, "accepted")
	require.Equal(t, "node-b", onA.Machine,
		"the storage node projected a proposal the other machine published")

	// And the other way, so neither direction is an accident of who stores what.
	fromA := propose(ctx, t, nodeA, `{"unit_type":3,"unit_id":10,"unit_type_name_advertised":"Stored"}`)
	onB := waitForRegistrationStatus(ctx, t, nodeB, fromA.ProposalID, "accepted")
	require.Equal(t, "node-a", onB.Machine)

	// Both machines answer with the same site state, because both folded the same
	// ordered journal. Two machines of one site do not disagree about the site.
	require.Equal(t, listRegistrations(ctx, t, nodeA), listRegistrations(ctx, t, nodeB))

	// The startup order the platform promises: the Event Fabric is ready before
	// the public API accepts anything. Both machines are still running here; the
	// lines being checked are startup lines, so they are long since written.
	for _, m := range []*machine{nodeA, nodeB} {
		logs := m.logs()
		fabricAt := strings.Index(logs, "event fabric")
		listeningAt := strings.Index(logs, "listening on")
		require.GreaterOrEqual(t, fabricAt, 0, "%s did not report its event fabric:\n%s", m.name, logs)
		require.GreaterOrEqual(t, listeningAt, 0, "%s did not report its API address:\n%s", m.name, logs)
		require.Less(t, fabricAt, listeningAt, "%s started its API before its event fabric:\n%s", m.name, logs)
	}
}

// journalOf reads the site journal name a machine reported at startup. It is
// derived from the deployment scope, so two machines of one site must agree.
func journalOf(t *testing.T, m *machine) string {
	t.Helper()
	const marker = "journal "
	logs := m.logs()
	tokens := logscan.After(logs, marker)
	require.NotEmpty(t, tokens, "%s did not name its journal:\n%s", m.name, logs)
	require.NotEmpty(t, tokens[0], "%s named an empty journal:\n%s", m.name, logs)
	return tokens[0]
}

// requireReported checks a running machine said something at startup, without
// stopping it to find out.
func requireReported(t *testing.T, m *machine, want string, because ...string) {
	t.Helper()
	require.Containsf(t, m.logs(), want, "%s did not report %q: %s\n%s",
		m.name, want, strings.Join(because, " "), m.logs())
}
