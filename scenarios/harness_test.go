package scenarios

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// This file is the scenario harness: how a scenario builds a project and runs
// the machines it produces. Everything here drives external processes the way a
// customer would, so a scenario never imports builder or platform code.

// scenarioSite is the site every scenario blueprint deploys to. Scenarios cover
// one site at a time, so the site is fixed here rather than threaded through
// every call. A scenario spanning two sites would take it as an argument again.
const scenarioSite = "local"

// buildProject drives the builder CLI to build every machine of a blueprint into
// outDir. It is the same command a customer runs.
func buildProject(ctx context.Context, t *testing.T, blueprintsDir, outDir, project string) {
	t.Helper()
	builderDir, err := filepath.Abs(filepath.Join("..", "builder"))
	require.NoError(t, err)

	// Flags must precede the positional project argument: Go's flag package
	// stops parsing flags at the first non-flag argument.
	build := exec.CommandContext(ctx, "go", "run", "./cmd/opdl", "build",
		"-examples", blueprintsDir, "-out", outDir, project)
	build.Dir = builderDir
	output, err := build.CombinedOutput()
	require.NoError(t, err, "builder build failed:\n%s", output)
}

// machineBinary returns the path of one built machine's binary. The builder
// names each package after the machine it is for.
func machineBinary(outDir, project, machine string) string {
	name := machine
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(outDir, project, scenarioSite, machine, name)
}

// machine is one running platform process under a scenario's control.
type machine struct {
	// name is the deployment machine identity.
	name string
	// url is the base URL of its registration API.
	url string
	// eventsDir is the directory it records events into.
	eventsDir string

	output  *bytes.Buffer
	cmd     *exec.Cmd
	stopped bool
}

// startMachine writes a configuration file for one built machine and starts it.
// The scenario picks the API address and events directory so several machines
// can run on one host without colliding.
func startMachine(ctx context.Context, t *testing.T, binaryPath, workDir, name string) *machine {
	t.Helper()
	require.FileExists(t, binaryPath)

	addr := freeAddress(t)
	eventsDir := filepath.Join(workDir, "events-"+name)
	configPath := filepath.Join(workDir, "config-"+name+".json")
	require.NoError(t, os.WriteFile(configPath, eventsConfig(addr, eventsDir), 0o644))

	m := &machine{name: name, url: "http://" + addr, eventsDir: eventsDir, output: &bytes.Buffer{}}
	m.cmd = exec.CommandContext(ctx, binaryPath, "-config", configPath)
	m.cmd.Stdout = m.output
	m.cmd.Stderr = m.output
	require.NoError(t, m.cmd.Start())
	t.Cleanup(m.stop)
	return m
}

// stop force-stops the machine. A graceful child interrupt is not portable, and
// a scenario reads the events it needs while the process is alive, so stopping
// hard is enough for cleanup.
func (m *machine) stop() {
	if m.stopped {
		return
	}
	m.stopped = true
	_ = m.cmd.Process.Kill()
	_ = m.cmd.Wait()
}

// logs returns the machine's captured output. It force-stops the process first:
// Wait joins the exec copier goroutines, so the buffer is only safe to read
// afterwards.
func (m *machine) logs() string {
	m.stop()
	return m.output.String()
}

// freeAddress reserves an ephemeral loopback port, then releases it so a machine
// can bind it. A brief race window is acceptable for a scenario.
func freeAddress(t *testing.T) string {
	t.Helper()
	var listen net.ListenConfig
	listener, err := listen.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())
	return addr
}
