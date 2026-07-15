package scenarios

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTwoMachineFabric is the smallest deployment that has to form a real
// fabric: two machines of one site, each built with nothing but its own
// descriptor, meeting without being told about each other at runtime.
//
// It checks only that both members start and form one fabric. Registration is
// still process-local, so nothing here shares state across the two.
func TestTwoMachineFabric(t *testing.T) {
	ctx := context.Background()
	scenariosDir, err := filepath.Abs(".")
	require.NoError(t, err)
	outDir := t.TempDir()
	buildProject(ctx, t, filepath.Join(scenariosDir, "testdata"), outDir, "two-machine")

	// The site's members, as the blueprint declares them. Each machine derives
	// its fabric addresses from its own IP, so both bind the fixed ports on one
	// host without any runtime override.
	nodeA := eventNode{
		Project: "two-machine", Environment: "development",
		Site: "local", Machine: "node-a", Role: "all-in-one",
	}
	nodeB := eventNode{
		Project: "two-machine", Environment: "development",
		Site: "local", Machine: "node-b", Role: "all-in-one",
	}

	// Start the first machine while the second is down. It must come up anyway:
	// a site has to boot in some order, and the machine that goes first cannot
	// wait for a peer that is not there yet.
	first := startMachine(ctx, t, machineBinary(outDir, "two-machine", "node-a"), outDir, "node-a")
	firstStarted := requireSingleEvent(t,
		waitForEventTypes(t, first.eventsDir, nodeA, "platform.fabric.started"),
		"platform.fabric.started")
	if got := firstStarted.payload(t)["members"]; got != float64(1) {
		t.Logf("first machine saw %v members at readiness; it started before its peer existed", got)
	}

	// Start the second machine. It seeds from the descriptor's peer address, so
	// by the time it reports ready it has already joined the first.
	second := startMachine(ctx, t, machineBinary(outDir, "two-machine", "node-b"), outDir, "node-b")
	secondStarted := requireSingleEvent(t,
		waitForEventTypes(t, second.eventsDir, nodeB, "platform.fabric.started"),
		"platform.fabric.started")

	// This is the proof that the two formed one fabric: the joiner sees both
	// members of the site. Nothing told it where the other machine was except
	// the topology compiled into it.
	require.Equal(t, float64(2), secondStarted.payload(t)["members"],
		"the second machine did not join the first: it reported its member count at readiness, having seeded from the descriptor's peer address")

	// Both members run the same adapter, and each recorded into its own log.
	for _, started := range []eventRecord{firstStarted, secondStarted} {
		require.Equal(t, "olric", started.payload(t)["adapter"])
		require.Equal(t, "fabric", started.Source)
	}

	// Each machine's log is its own: the events are separated by the deployment
	// identity each machine was built with, not by anything observed at runtime.
	requireOnlyNodeInLog(t, first.eventsDir, nodeA)
	requireOnlyNodeInLog(t, second.eventsDir, nodeB)

	// The startup order the platform promises: the fabric is ready before the
	// public API accepts anything.
	for _, m := range []*machine{first, second} {
		logs := m.logs()
		fabricAt := strings.Index(logs, "fabric member")
		listeningAt := strings.Index(logs, "listening on")
		require.GreaterOrEqual(t, fabricAt, 0, "%s did not report its fabric membership:\n%s", m.name, logs)
		require.GreaterOrEqual(t, listeningAt, 0, "%s did not report its API address:\n%s", m.name, logs)
		require.Less(t, fabricAt, listeningAt, "%s started its API before its fabric:\n%s", m.name, logs)
	}
}
