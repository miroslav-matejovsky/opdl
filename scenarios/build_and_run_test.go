package scenarios

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestBuildAndRunSingleMachine is the bare-minimum end-to-end scenario: drive
// the builder CLI to build the single machine in the "scenario" example
// blueprint, then run the resulting platform binary and check it starts its API,
// reports its configuration, and answers a GET request. Both are external
// processes; nothing here imports builder or platform Go code.
func TestBuildAndRunSingleMachine(t *testing.T) {
	ctx := context.Background()
	scenariosDir, err := filepath.Abs(".")
	require.NoError(t, err)
	blueprintsDir := filepath.Join(scenariosDir, "testdata")
	builderDir := filepath.Join(scenariosDir, "..", "builder")
	outDir := t.TempDir()

	// Flags must precede the positional project argument: Go's flag package
	// stops parsing flags at the first non-flag argument.
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

	// Point the binary at a JSON configuration file that pins a free loopback
	// address, so the scenario controls where the API listens without colliding
	// with other tests.
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

	url := "http://" + addr + "/"
	var body string
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
		if resp.StatusCode != http.StatusOK {
			return false
		}
		b, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return false
		}
		body = string(b)
		return true
	}, 15*time.Second, 100*time.Millisecond, "platform API never became reachable")

	require.Contains(t, body, "platform is running")

	// Stop the process before reading its captured output: Wait joins the exec
	// copier goroutines, so the buffer is safe to read only afterwards.
	stop()
	require.Contains(t, out.String(), "platform configuration")
}

// freePort reserves an ephemeral port, then releases it so the platform can bind
// it. A brief race window is acceptable for a scenario test.
func freePort(t *testing.T) int {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	return port
}
