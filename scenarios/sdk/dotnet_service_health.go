package sdk

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/scenarios/internal/harness"
	"github.com/miroslav-matejovsky/opdl/scenarios/internal/procrun"
)

// The tests this scenario drives. They are named here rather than counted
// because a count would pass on the wrong tests running: what makes the run
// meaningful is that these two, which skip without an address to reach, did not
// skip.
var expectedTests = []string{
	"Opdl.Sdk.E2E.ServiceHealthTests.ServiceViewDeserializesIntoTheGeneratedModels",
	"Opdl.Sdk.E2E.ServiceHealthTests.FailingTargetsDoNotMakeTheInstanceUnhealthy",
}

// DotnetServiceHealth drives a running instance's service health view through
// the generated .NET SDK, as a consumer would.
//
// The subject is the generated client. GET /health/services is the platform's
// first nested response — a list of services, each carrying a list of what its
// observers found — and a contract that generates into unusable models fails
// only here: the Go tests on one side and the OpenAPI document on the other both
// pass over a client nobody can call.
//
// The service the machine is authored with has nothing listening behind it, so
// the instance is watching a target it cannot reach for the whole scenario. That
// is deliberate. It is the arrangement in which the coupling the design forbids
// would show: if a failing target reached platform health, this instance would
// report itself Unhealthy, and the SDK test asserts it does not.
//
// Nothing here imports builder, platform, or SDK code. The machine and the .NET
// test are external processes, exactly as a user would run them.
func DotnetServiceHealth(t *testing.T) {
	dotnet, err := exec.LookPath("dotnet")
	if err != nil {
		t.Skip("dotnet not installed; skipping the dotnet SDK scenario")
	}
	// After the skip, so a host without dotnet reports it immediately rather than
	// parking the scenario until the serial phase ends.
	t.Parallel()

	ctx := t.Context()
	scenariosDir, err := filepath.Abs(".")
	require.NoError(t, err)

	outDir := filepath.Join(harness.ScenarioDir(t), "out")
	deployment := harness.DeploySite(ctx, t, outDir, filepath.Join(harness.ScenarioDir(t), "work"), "simple")
	node := deployment.Machine(t, "node-a")
	deployment.StartSite(ctx, t)

	project := filepath.Join(scenariosDir, "..", "sdk-dotnet", "tests", "Opdl.Sdk.E2E", "Opdl.Sdk.E2E.csproj")
	command := exec.CommandContext(ctx, dotnet, "test", project,
		"--nologo", "--verbosity", "quiet", "--logger", "console;verbosity=normal")
	command.Env = append(os.Environ(), "OPDL_PLATFORM_BASEURL="+node.URL)

	sdkTest, err := procrun.Start(command)
	require.NoError(t, err)
	// The child is a build and a test host, so it is taken down with the scenario
	// rather than left to finish on its own if this fails early.
	t.Cleanup(func() {
		_ = sdkTest.Kill()
		_, _ = sdkTest.Wait()
	})

	output, err := sdkTest.Wait()
	require.NoErrorf(t, err, "dotnet SDK tests failed:\n%s%s", output, harness.Diagnostics(node))
	// A skipped test is a passing test run, and skipping is exactly what these do
	// without an address to reach. Asserting on the pass is what makes the run
	// prove something rather than merely finish.
	for _, name := range expectedTests {
		require.Containsf(t, output, "Passed "+name,
			"%s did not run to a pass (skipped, or never discovered?):\n%s", name, output)
	}
}
