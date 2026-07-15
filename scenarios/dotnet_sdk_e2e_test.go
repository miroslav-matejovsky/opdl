package scenarios

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestDotnetSDKEndToEnd is the full-stack scenario: build a platform binary from a
// blueprint with the builder, start it, then run the .NET SDK's end-to-end tests
// against the live process. It exercises the whole chain a real integration would
// use: the builder, the platform runtime, the OpenAPI contract, and the
// Kiota-generated .NET client a consumer calls.
//
// Nothing here imports builder, platform, or SDK
// code: the builder, the platform, and the .NET tests are all driven as external
// processes, exactly as a user would.
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
	buildProject(ctx, t, filepath.Join(scenariosDir, "testdata"), outDir, "scenario")

	platform := startMachine(ctx, t, machineBinary(outDir, "scenario", "node"), outDir, "node")
	addr := strings.TrimPrefix(platform.url, "http://")

	// Wait until the platform answers before handing off to the .NET tests, so they
	// do not have to carry their own start-up retry.
	url := "http://" + addr + "/registrations"
	require.Eventually(t, func() bool {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		if reqErr != nil {
			return false
		}
		resp, getErr := http.DefaultClient.Do(req)
		if getErr != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode == http.StatusOK
	}, 15*time.Second, 100*time.Millisecond, "platform API never became reachable")

	// Run the .NET SDK end-to-end tests against the live platform. The project
	// reads the address from OPDL_PLATFORM_BASEURL and would skip without it, so
	// require the summary to report a pass, not a skip, to prove it actually ran.
	e2eProject := filepath.Join(scenariosDir, "..", "sdk-dotnet", "tests", "Opdl.Sdk.E2E", "Opdl.Sdk.E2E.csproj")
	test := exec.CommandContext(ctx, dotnet, "test", e2eProject, "--nologo", "--verbosity", "quiet")
	test.Env = append(os.Environ(), "OPDL_PLATFORM_BASEURL=http://"+addr)
	testOut, err := test.CombinedOutput()
	require.NoErrorf(t, err, "dotnet SDK end-to-end tests failed:\n%s", testOut)
	require.Truef(t, strings.Contains(string(testOut), "Passed!"),
		"dotnet SDK end-to-end tests did not run to a pass (skipped or empty?):\n%s", testOut)
}
