package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
// blueprint, then run the resulting platform binary and check it starts its
// registration API, persists a request, and reports its configuration. Both are external
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

	url := "http://" + addr
	require.Eventually(t, func() bool {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, url+"/registrations", http.NoBody)
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
		var registrations []registration
		return json.NewDecoder(resp.Body).Decode(&registrations) == nil && len(registrations) == 0
	}, 15*time.Second, 100*time.Millisecond, "platform API never became reachable")

	request := []byte(`{"unit_type":7,"unit_id":42,"unit_type_name_advertised":"Scenario service","role":"Master"}`)
	post, err := http.NewRequestWithContext(ctx, http.MethodPost, url+"/registrations", bytes.NewReader(request))
	require.NoError(t, err)
	post.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(post)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusAccepted, response.StatusCode)

	var status registration
	require.Eventually(t, func() bool {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, url+"/registrations/7/42/status", http.NoBody)
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
		return json.NewDecoder(resp.Body).Decode(&status) == nil && status.Status == "accepted"
	}, 15*time.Second, 100*time.Millisecond, "registration was not accepted")
	require.Equal(t, "node", status.Machine)
	require.Equal(t, "127.0.0.1", status.IP)
	require.Len(t, status.PlatformInstances, 1)
	require.Equal(t, "node", status.PlatformInstances[0].Machine)
	require.Equal(t, "127.0.0.1", status.PlatformInstances[0].IP)
	require.Equal(t, "accepted", status.PlatformInstances[0].Status)

	list, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/registrations", http.NoBody)
	require.NoError(t, err)
	response, err = http.DefaultClient.Do(list)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var registrations []registration
	require.NoError(t, json.NewDecoder(response.Body).Decode(&registrations))
	require.Equal(t, []registration{status}, registrations)

	// Stop the process before reading its captured output: Wait joins the exec
	// copier goroutines, so the buffer is safe to read only afterwards.
	stop()
	require.Contains(t, out.String(), "platform configuration")
}

type registration struct {
	UnitType          uint8  `json:"unit_type"`
	UnitID            uint16 `json:"unit_id"`
	Machine           string `json:"machine"`
	IP                string `json:"ip"`
	Status            string `json:"status"`
	PlatformInstances []struct {
		Machine string `json:"machine"`
		IP      string `json:"ip"`
		Status  string `json:"status"`
	} `json:"platform_instances"`
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
