package scenarios

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
// The platform API is deliberately trivial for now (a single status endpoint); the
// point of this scenario is to wire the pieces together end to end so a real API
// can grow behind the same seams. Nothing here imports builder, platform, or SDK
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
	blueprintsDir := filepath.Join(scenariosDir, "testdata")
	builderDir := filepath.Join(scenariosDir, "..", "builder")
	outDir := t.TempDir()

	// Build the single machine in the "scenario" blueprint. Flags must precede the
	// positional project argument: Go's flag package stops at the first non-flag.
	build := exec.CommandContext(ctx, "go", "run", "./cmd/opdl", "build",
		"-examples", blueprintsDir, "-out", outDir, "scenario")
	build.Dir = builderDir
	output, err := build.CombinedOutput()
	require.NoError(t, err, "builder build failed:\n%s", output)

	binaryName := "node"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(outDir, "scenario", "local", "node", binaryName)
	require.FileExists(t, binaryPath)

	// Pin a free loopback address so the SDK knows where to reach the platform.
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	configPath := filepath.Join(outDir, "config.json")
	require.NoError(t, os.WriteFile(configPath, fmt.Appendf(nil, `{"address": %q}`, addr), 0o644))

	var out bytes.Buffer
	run := exec.CommandContext(ctx, binaryPath, "-config", configPath)
	run.Stdout = &out
	run.Stderr = &out
	require.NoError(t, run.Start())
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = run.Process.Kill()
		_ = run.Wait()
	}
	defer stop()

	// Wait until the platform answers before handing off to the .NET tests, so they
	// do not have to carry their own start-up retry.
	url := "http://" + addr + "/"
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
