package scenarios

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// pendingObservedMarker is the file the .NET test writes once it has proven that
// node A reports the proposal as pending while node B is offline. It is this
// scenario's cue to start node B.
//
// It is a handshake rather than a delay because the fact being waited for is
// another process finishing an assertion. Its name is shared with
// sdk-dotnet/tests/Opdl.Sdk.E2E/RegistrationTests.cs.
const pendingObservedMarker = "pending-observed"

// TestDotnetSDKEndToEnd is the first use case as a customer meets it, with
// nothing simulated: the builder builds two machines from one blueprint, both
// run as real processes over a real site journal, and a .NET consumer drives them
// through the generated SDK.
//
// It proves the acceptance barrier from the outside. Node A takes a proposal
// while node B is deliberately not running, and the platform must keep it pending
// and name node B as what it is waiting for. Only once the .NET test has asserted
// that, and said so through a file marker, does this scenario start node B and
// let the site accept.
//
// The SDK is the subject, not just the transport: the contract is asynchronous,
// so what is being checked is that a generated client can take a proposal's
// identity from a 202 and follow it to a decision. The platform's own public
// projection is the second account of the same facts, and a scenario that only
// checked one of them would not notice the other drifting.
//
// Nothing here imports builder, platform, or SDK code. The builder, both
// machines, and the .NET test are external processes, exactly as a user would run
// them.
func TestDotnetSDKEndToEnd(t *testing.T) {
	dotnet, err := exec.LookPath("dotnet")
	if err != nil {
		t.Skip("dotnet not installed; skipping dotnet SDK end-to-end scenario")
	}

	ctx := t.Context()
	scenariosDir, err := filepath.Abs(".")
	require.NoError(t, err)
	outDir := filepath.Join(scenarioDir(t), "out")
	// Both machines are prepared up front so the .NET test knows where node B will
	// answer, and started separately so node B is genuinely absent while the
	// pending assertions run.
	deployment := deploySite(ctx, t, outDir, filepath.Join(scenarioDir(t), "work"), "two-machine")
	first, second := deployment.machine(t, "node-a"), deployment.machine(t, "node-b")
	controlDir := filepath.Join(scenarioDir(t), "control")

	first.start(ctx, t)
	waitForAPI(ctx, t, first)

	// The .NET test runs asynchronously: it blocks partway through waiting for
	// node B, so this scenario has to still be running to start it.
	e2eProject := filepath.Join(scenariosDir, "..", "sdk-dotnet", "tests", "Opdl.Sdk.E2E", "Opdl.Sdk.E2E.csproj")
	command := exec.CommandContext(ctx, dotnet, "test", e2eProject, "--nologo", "--verbosity", "quiet", "--logger", "console;verbosity=normal")
	command.Env = append(os.Environ(),
		"OPDL_PLATFORM_BASEURL_A="+first.url,
		"OPDL_PLATFORM_BASEURL_B="+second.url,
		"OPDL_CONTROL_DIR="+controlDir,
	)
	sdkTest := startProcess(t, command)

	// Node B starts only once the SDK test has proven pending behavior.
	waitForMarker(t, controlDir, pendingObservedMarker, sdkTest, func() string {
		return diagnose([]*machine{first, second}) + "\n--- dotnet SDK test output so far ---\n" + sdkTest.logs()
	})
	second.start(ctx, t)

	testOut, err := sdkTest.wait()
	require.NoErrorf(t, err, "dotnet SDK end-to-end tests failed:\n%s%s",
		testOut, diagnostics(first, second))
	// The tests skip without their environment variables, so a run that skipped
	// would otherwise pass while proving nothing.
	require.Containsf(t, testOut, "Passed Opdl.Sdk.E2E.RegistrationTests.RegistrationIsAcceptedOnlyAfterEveryExpectedMachineConfirms",
		"dotnet SDK end-to-end tests did not run to a pass (skipped or empty?):\n%s", testOut)

	// The platform's public projection and the SDK's account agree about what
	// happened. The SDK drove the site to one accepted registration and two losing
	// claims for the same key; both machines report exactly that.
	for _, m := range []*machine{first, second} {
		registrations := listRegistrations(ctx, t, m)
		require.Len(t, registrations, 3, "%s: one winner and two losing claims", m.name)

		accepted := make([]registration, 0, 1)
		for _, view := range registrations {
			if view.Status == "accepted" {
				accepted = append(accepted, view)
			}
		}
		require.Len(t, accepted, 1, "%s: exactly one claim holds the key", m.name)
		require.Equal(t, "node-a", accepted[0].Machine, "%s: the origin is where the client asked", m.name)
		require.Equal(t, "Billing", accepted[0].UnitTypeNameAdvertised)
		require.Equal(t, "accepted", accepted[0].instance(t, "node-a").Status)
		require.Equal(t, "accepted", accepted[0].instance(t, "node-b").Status,
			"%s: the barrier held until node-b confirmed", m.name)

		conflicts := listConflicts(ctx, t, m)
		require.Len(t, conflicts, 1, "%s: one contested key", m.name)
		require.Equal(t, accepted[0].ProposalID, conflicts[0].Winner.ProposalID,
			"%s: the incumbent survived both later claims", m.name)
		require.Len(t, conflicts[0].Losers, 2)
	}
}
