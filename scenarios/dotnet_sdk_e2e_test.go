package scenarios

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// pendingObservedMarker is the file the .NET test writes once it has proven that
// node A reports the request as pending while node B is offline. It is this
// scenario's cue to start node B.
//
// It is a handshake rather than a delay because the fact being waited for is
// another process finishing an assertion. Its name is shared with
// sdk-dotnet/tests/Opdl.Sdk.E2E/RegistrationTests.cs.
const pendingObservedMarker = "pending-observed"

// TestDotnetSDKEndToEnd is the first use case as a customer meets it, with
// nothing simulated: the builder builds two machines from one blueprint, both run
// as real processes over a real fabric, and a .NET consumer drives them through
// the generated SDK.
//
// It proves the acceptance barrier from the outside. Node A takes a request while
// node B is deliberately not running, and the platform must keep the request
// pending and name node B as what it is waiting for. Only once the .NET test has
// asserted that, and said so through a file marker, does this scenario start node
// B and let the site accept.
//
// The platform's own event logs are the evidence, not its process output: a
// registration's phases are facts the platform states, and this checks that the
// story they tell matches the one the SDK was told.
//
// Nothing here imports builder, platform, or SDK code. The builder, both
// machines, and the .NET test are external processes, exactly as a user would run
// them.
func TestDotnetSDKEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping dotnet SDK end-to-end scenario in -short mode")
	}
	dotnet, err := exec.LookPath("dotnet")
	if err != nil {
		t.Skip("dotnet not installed; skipping dotnet SDK end-to-end scenario")
	}

	ctx := context.Background()
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

	// Both machines are prepared up front so the .NET test knows where node B
	// will answer, and started separately so node B is genuinely absent while the
	// pending assertions run.
	first := prepareMachine(t, machineBinary(outDir, "two-machine", "node-a"), outDir, "node-a")
	second := prepareMachine(t, machineBinary(outDir, "two-machine", "node-b"), outDir, "node-b")
	controlDir := t.TempDir()

	first.start(ctx, t)
	waitForAPI(ctx, t, first)

	// The .NET test runs asynchronously: it blocks partway through waiting for
	// node B, so this scenario has to still be running to start it.
	e2eProject := filepath.Join(scenariosDir, "..", "sdk-dotnet", "tests", "Opdl.Sdk.E2E", "Opdl.Sdk.E2E.csproj")
	command := exec.CommandContext(ctx, dotnet, "test", e2eProject, "--nologo", "--verbosity", "quiet")
	command.Env = append(os.Environ(),
		"OPDL_PLATFORM_BASEURL_A="+first.url,
		"OPDL_PLATFORM_BASEURL_B="+second.url,
		"OPDL_CONTROL_DIR="+controlDir,
	)
	sdkTest := startProcess(t, command)

	// Node B starts only once the SDK test has proven pending behavior.
	waitForMarker(t, controlDir, pendingObservedMarker, sdkTest, func() string {
		return diagnose(t, []*machine{first, second}, sdkTest)
	})
	second.start(ctx, t)

	testOut, err := sdkTest.wait()
	require.NoErrorf(t, err, "dotnet SDK end-to-end tests failed:\n%s\n%s",
		testOut, diagnose(t, []*machine{first, second}, sdkTest))
	// The tests skip without their environment variables, so a run that skipped
	// would otherwise pass while proving nothing.
	require.Containsf(t, testOut, "Passed!",
		"dotnet SDK end-to-end tests did not run to a pass (skipped or empty?):\n%s", testOut)

	// The event logs are read while both processes are alive. Each record is
	// flushed before the operation that produced it answers, so the logs are
	// already complete now, and a force stop is not portable enough to rely on
	// for flushing.
	requireOnlyNodeInLog(t, first.eventsDir, nodeA)
	requireOnlyNodeInLog(t, second.eventsDir, nodeB)
	originEvents := readEvents(t, first.eventsDir, nodeA)
	peerEvents := readEvents(t, second.eventsDir, nodeB)

	// Both machines formed the fabric before serving anything.
	require.Equal(t, "olric",
		requireSingleEvent(t, originEvents, "platform.fabric.started").payload(t)["adapter"])
	require.Equal(t, "olric",
		requireSingleEvent(t, peerEvents, "platform.fabric.started").payload(t)["adapter"])

	// The origin took the request once. The exact retry changed nothing, so it
	// added nothing.
	requested := requireSingleEvent(t, originEvents, "platform.registration.requested")
	require.Equal(t, "registration", requested.Source)
	require.Equal(t, float64(7), requested.payload(t)["unit_type"])
	require.Equal(t, float64(42), requested.payload(t)["unit_id"])
	require.Equal(t, "Billing", requested.payload(t)["unit_type_name_advertised"])
	require.Equal(t, "node-a", requested.payload(t)["machine"], "the origin is the platform's own identity")
	require.Equal(t, "127.0.0.1", requested.payload(t)["ip"])
	require.NotContains(t, requested.payload(t), "role", "a role-less request advertises no role")

	// Each machine confirmed the request in its own log, naming itself and the
	// request's origin.
	//
	// Node-a may confirm twice because node-b joins a site that already holds
	// node-a's confirmation. Olric can briefly report an existing key as absent
	// during that join, so the byte-identical record is created and stated again.
	// Registration correctness comes from retained contenders and reconciliation,
	// not exactly-once transition events. The query API is authoritative after
	// convergence; see docs/01-architecture.md.
	originConfirmed := requireFirstEvent(t, originEvents, "platform.registration.confirmed")
	require.Equal(t, "node-a", originConfirmed.payload(t)["confirming_machine"])
	require.Equal(t, "node-a", originConfirmed.payload(t)["origin_machine"])
	for _, confirmed := range eventsOfType(originEvents, "platform.registration.confirmed") {
		require.Equal(t, "node-a", confirmed.payload(t)["confirming_machine"],
			"node-a states its own answer, never another machine's")
	}

	peerConfirmed := requireFirstEvent(t, peerEvents, "platform.registration.confirmed")
	require.Equal(t, "node-b", peerConfirmed.payload(t)["confirming_machine"])
	require.Equal(t, "node-a", peerConfirmed.payload(t)["origin_machine"],
		"a machine that is not the origin still states whose request it confirmed")
	require.Equal(t, []string{"platform.fabric.started", "platform.registration.confirmed"},
		eventTypes(peerEvents[:2]), "node-b confirms; it does not take or accept another machine's request")

	// Acceptance happened once, on the origin, and only after both confirmations.
	accepted := requireSingleEvent(t, originEvents, "platform.registration.accepted")
	require.Equal(t, "node-a", accepted.payload(t)["machine"])
	require.Equal(t, "127.0.0.1", accepted.payload(t)["ip"])
	require.Equal(t, "Billing", accepted.payload(t)["unit_type_name_advertised"])
	require.Greater(t, accepted.OccurredAt.UnixNano(), originConfirmed.OccurredAt.UnixNano(),
		"the origin accepted before it had confirmed")
	require.Greater(t, accepted.OccurredAt.UnixNano(), peerConfirmed.OccurredAt.UnixNano(),
		"the origin accepted before node-b had confirmed: the barrier did not hold")
	requireEventOrder(t, originEvents,
		"platform.registration.requested",
		"platform.registration.confirmed",
		"platform.registration.accepted",
	)

	// Two refused claims, one per machine, each stated as a warning by the
	// machine that refused it and carrying both sides of the difference.
	sameMachine := requireSingleEvent(t, originEvents, "platform.registration.conflict")
	require.Contains(t, sameMachine.Tags, "warning")
	require.Equal(t, "registration_key_conflict", sameMachine.payload(t)["reason"])
	require.Equal(t, "Billing", existingField(t, sameMachine, "unit_type_name_advertised"))
	require.Equal(t, "Payments", attemptedField(t, sameMachine, "unit_type_name_advertised"))
	require.Equal(t, "node-a", attemptedField(t, sameMachine, "machine"))

	crossMachine := requireSingleEvent(t, peerEvents, "platform.registration.conflict")
	require.Contains(t, crossMachine.Tags, "warning")
	require.Equal(t, "registration_key_conflict", crossMachine.payload(t)["reason"])
	require.Equal(t, "node-a", existingField(t, crossMachine, "machine"),
		"the key is held by node-a, whoever attempted to reuse it")
	require.Equal(t, "127.0.0.1", existingField(t, crossMachine, "ip"))
	require.Equal(t, "node-b", attemptedField(t, crossMachine, "machine"))
	require.Equal(t, "127.0.0.2", attemptedField(t, crossMachine, "ip"))

	// The API's final answer and the events agree about what happened. They are
	// independent accounts of the same facts, and a scenario that only checked
	// one of them would not notice the other drifting.
	final, code := getRegistration(ctx, t, first, 7, 42)
	require.Equal(t, 200, code)
	require.Equal(t, "accepted", final.Status)
	require.Equal(t, "node-a", final.Machine)
	require.Equal(t, "accepted", final.instance(t, "node-a").Status)
	require.Equal(t, "accepted", final.instance(t, "node-b").Status)
	require.Equal(t, []registration{final}, listRegistrations(ctx, t, second),
		"both machines report the registration the events describe")
}

// existingField reads one field of a conflict event's existing claim.
func existingField(t *testing.T, record eventRecord, field string) any {
	t.Helper()
	return conflictSide(t, record, "existing")[field]
}

// attemptedField reads one field of a conflict event's refused claim.
func attemptedField(t *testing.T, record eventRecord, field string) any {
	t.Helper()
	return conflictSide(t, record, "attempted")[field]
}

// conflictSide reads one side of a conflict event's payload.
func conflictSide(t *testing.T, record eventRecord, side string) map[string]any {
	t.Helper()
	value, ok := record.payload(t)[side].(map[string]any)
	require.Truef(t, ok, "conflict payload has no %s object: %v", side, record.payload(t))
	return value
}

// diagnose renders everything worth knowing when a two-node scenario fails: what
// each machine printed, where its events are and what is in them, where it
// answers, and what the .NET test has said so far.
//
// It is only ever called on a failure path. A passing run says nothing, because
// the point of a scenario that passes is that nobody has to read it.
func diagnose(t *testing.T, machines []*machine, sdkTest *process) string {
	t.Helper()
	var b strings.Builder
	for _, m := range machines {
		state := "running"
		if !m.running() {
			state = "not started"
		}
		fmt.Fprintf(&b, "\n--- machine %s (%s, api %s) ---\n%s", m.name, state, m.url, m.output.String())
		fmt.Fprintf(&b, "\n--- machine %s events (%s) ---\n", m.name, m.eventsDir)
		entries, err := filepath.Glob(filepath.Join(m.eventsDir, "*.jsonl"))
		if err != nil || len(entries) == 0 {
			fmt.Fprintf(&b, "(no event log)\n")
			continue
		}
		for _, entry := range entries {
			data, readErr := os.ReadFile(entry)
			if readErr != nil {
				fmt.Fprintf(&b, "%s: %v\n", entry, readErr)
				continue
			}
			fmt.Fprintf(&b, "%s:\n%s\n", entry, data)
		}
	}
	if sdkTest != nil {
		fmt.Fprintf(&b, "\n--- dotnet SDK test output so far ---\n%s\n", sdkTest.logs())
	}
	return b.String()
}
