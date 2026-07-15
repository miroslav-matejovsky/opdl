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
	"time"

	"github.com/stretchr/testify/require"
)

// This file is the scenario harness: how a scenario builds a project and runs
// the machines it produces. Everything here drives external processes the way a
// customer would, so a scenario never imports builder or platform code.

// scenarioSite is the site every scenario blueprint deploys to. Scenarios cover
// one site at a time, so the site is fixed here rather than threaded through
// every call. A scenario spanning two sites would take it as an argument again.
const scenarioSite = "local"

const (
	// markerPollInterval is how often a handshake re-checks for a marker.
	markerPollInterval = 50 * time.Millisecond
	// markerWaitTimeout bounds a handshake. It is generous because the process
	// on the other side is starting a .NET test host before it can reach the
	// point it signals from.
	markerWaitTimeout = 90 * time.Second
)

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

// machine is one platform process under a scenario's control: prepared, and
// running once started.
type machine struct {
	// name is the deployment machine identity.
	name string
	// url is the base URL of its registration API, known from the moment it is
	// prepared, whether or not it is running.
	url string
	// eventsDir is the directory it records events into.
	eventsDir string

	binaryPath string
	configPath string
	output     *bytes.Buffer
	cmd        *exec.Cmd
	stopped    bool
}

// running reports whether the machine has been started.
func (m *machine) running() bool { return m.cmd != nil }

// prepareMachine writes a configuration file for one built machine and reserves
// its API address, without starting it. The scenario picks the API address and
// events directory so several machines can run on one host without colliding.
//
// Preparing and starting are separate so a scenario can know where a machine
// will answer before it is running. That is what lets a machine be deliberately
// offline for part of a scenario while something else is already configured to
// call it, which is the only way to observe what the platform does about an
// expected machine that is not there.
func prepareMachine(t *testing.T, binaryPath, workDir, name string) *machine {
	t.Helper()
	require.FileExists(t, binaryPath)

	addr := freeAddress(t)
	eventsDir := filepath.Join(workDir, "events-"+name)
	configPath := filepath.Join(workDir, "config-"+name+".json")
	require.NoError(t, os.WriteFile(configPath, eventsConfig(addr, eventsDir), 0o644))

	return &machine{
		name:       name,
		url:        "http://" + addr,
		eventsDir:  eventsDir,
		binaryPath: binaryPath,
		configPath: configPath,
		output:     &bytes.Buffer{},
	}
}

// start runs a prepared machine and registers its cleanup.
func (m *machine) start(ctx context.Context, t *testing.T) {
	t.Helper()
	require.Nil(t, m.cmd, "%s is already started", m.name)
	m.cmd = exec.CommandContext(ctx, m.binaryPath, "-config", m.configPath)
	m.cmd.Stdout = m.output
	m.cmd.Stderr = m.output
	require.NoError(t, m.cmd.Start())
	t.Cleanup(m.stop)
}

// startMachine prepares one built machine and starts it.
func startMachine(ctx context.Context, t *testing.T, binaryPath, workDir, name string) *machine {
	t.Helper()
	m := prepareMachine(t, binaryPath, workDir, name)
	m.start(ctx, t)
	return m
}

// stop force-stops the machine. A graceful child interrupt is not portable, and
// a scenario reads the events it needs while the process is alive, so stopping
// hard is enough for cleanup. The orderly shutdown a signal would cause, and the
// order it releases dependencies in, are covered by the platform's in-process
// lifecycle tests instead.
func (m *machine) stop() {
	if m.stopped || !m.running() {
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

// waitForMarker blocks until name appears in dir, which is how a scenario waits
// for something inside another process to reach a point.
//
// It is a handshake rather than a sleep on purpose: the thing being waited for
// is another process deciding it has finished an assertion, and no duration
// expresses that. A sleep long enough to usually work is also a sleep that
// sometimes does not, and that cost is paid on every run forever.
//
// signaller is the process expected to write the marker. If it exits first the
// wait fails immediately rather than burning the timeout: a marker that will
// never be written is worth reporting now, with the output that explains why.
func waitForMarker(t *testing.T, dir, name string, signaller *process, describe func() string) {
	t.Helper()
	path := filepath.Join(dir, name)
	deadline := time.Now().Add(markerWaitTimeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if signaller.exited() {
			output, err := signaller.wait()
			require.FailNowf(t, "the process that signals "+name+" exited before writing it",
				"exit: %v\n%s\n%s", err, output, describe())
		}
		if time.Now().After(deadline) {
			require.FailNowf(t, name+" marker never appeared",
				"waited %s for %s\n%s", markerWaitTimeout, path, describe())
		}
		time.Sleep(markerPollInterval)
	}
}

// process is an external command a scenario runs and later collects.
type process struct {
	output *bytes.Buffer
	// finished closes once the command has exited and err is set, which is what
	// publishes err to every other goroutine.
	finished chan struct{}
	err      error
}

// startProcess runs cmd in the background, capturing its output.
//
// A scenario starts a long-running child asynchronously when it has to keep
// serving that child while it runs. The .NET test is the case that needs it: it
// blocks partway through waiting for this harness to start a machine, so a
// harness that waited for the test to exit first would deadlock.
func startProcess(t *testing.T, cmd *exec.Cmd) *process {
	t.Helper()
	p := &process{output: &bytes.Buffer{}, finished: make(chan struct{})}
	cmd.Stdout = p.output
	cmd.Stderr = p.output
	require.NoError(t, cmd.Start())
	go func() {
		p.err = cmd.Wait()
		close(p.finished)
	}()
	t.Cleanup(func() {
		// Whatever went wrong, the child does not outlive the scenario, and the
		// scenario does not return while it is still writing to the buffer.
		_ = cmd.Process.Kill()
		p.wait()
	})
	return p
}

// wait blocks until the process exits, returning its complete output and its
// exit error. Waiting joins the exec copier goroutines, so the buffer is only
// read afterwards. It is safe to call more than once.
func (p *process) wait() (string, error) {
	<-p.finished
	return p.output.String(), p.err
}

// exited reports whether the process has already finished, without blocking.
func (p *process) exited() bool {
	select {
	case <-p.finished:
		return true
	default:
		return false
	}
}

// logs returns what the process has written so far. It is for diagnostics on a
// path where the process may still be running, so it is deliberately a snapshot
// rather than the complete output wait returns.
func (p *process) logs() string { return p.output.String() }

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
