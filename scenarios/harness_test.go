package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
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

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		fmt.Println("skipping scenario suite in -short mode")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

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

type slotLaunch struct {
	Slot string   `json:"slot"`
	Args []string `json:"args"`
}

type packageManifest struct {
	Slots            []slotLaunch `json:"slots"`
	ShutdownStrategy string       `json:"shutdown_strategy"`
}

func readManifest(t *testing.T, binaryPath string) packageManifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(binaryPath), "manifest.json"))
	require.NoError(t, err)
	var manifest packageManifest
	require.NoError(t, json.Unmarshal(data, &manifest))
	require.NotEmpty(t, manifest.Slots)
	return manifest
}

func manifestSlot(t *testing.T, binaryPath, slot string) slotLaunch {
	t.Helper()
	for _, launch := range readManifest(t, binaryPath).Slots {
		if launch.Slot == slot {
			return launch
		}
	}
	require.FailNowf(t, "missing manifest slot", "slot %s is not in %s", slot, binaryPath)
	return slotLaunch{}
}

// sockets are one machine's reserved addresses and its journal storage.
//
// A deployment derives all of these from the machine's own IP on fixed ports.
// A scenario cannot: several machines share one host, and a developer's machine
// may already be using those ports. So the harness reserves ephemeral ports and
// hands them to the platform as runtime overrides, which move sockets and
// nothing else. Which machine a process is remains what it was built with.
type sockets struct {
	api         string
	client      string
	cluster     string
	monitor     string
	dataDir     string
	instanceDir string
}

// site is the machines of one built project under a scenario's control.
//
// Every machine is prepared before any is started, because each one's
// configuration names the others: the site's NATS nodes route to each other's
// cluster addresses, and on one host those addresses are not the ones the
// deployment derived. Preparing the site as a whole is what lets a scenario
// start the machines in any order, or leave one deliberately absent.
type site struct {
	project  string
	outDir   string
	workDir  string
	machines []*machine
}

// prepareSite builds nothing and starts nothing: it reserves each named
// machine's sockets and storage, and writes each one a configuration that routes
// to the others.
func prepareSite(t *testing.T, outDir, workDir, project string, names ...string) *site {
	t.Helper()
	s := &site{project: project, outDir: outDir, workDir: workDir}

	// Every address is reserved before any is released, so no two machines of the
	// site are handed the same port.
	addrs := freeAddresses(t, 4*len(names))
	reserved := make([]sockets, len(names))
	for i, name := range names {
		reserved[i] = sockets{
			api:         addrs[4*i],
			client:      addrs[4*i+1],
			cluster:     addrs[4*i+2],
			monitor:     addrs[4*i+3],
			dataDir:     filepath.Join(workDir, "nats-"+name),
			instanceDir: filepath.Join(workDir, "instance-"+name),
		}
	}

	// The site's storage nodes are chosen by sorted machine name, and a scenario
	// declares its machines in that order, so the first is the storage node of a
	// site smaller than three machines. Only storage nodes run a server; the rest
	// reach the journal as clients of theirs.
	//
	// The harness has to know which is which because it hands out the addresses.
	// It derives that the same way the platform does rather than being told,
	// so a scenario cannot quietly disagree with the deployment about who stores
	// what.
	storage := storageNodes(names)
	servers := make([]string, 0, len(storage))
	for _, i := range storage {
		servers = append(servers, reserved[i].client)
	}

	for i, name := range names {
		var routes []string
		if slices.Contains(storage, i) {
			routes = make([]string, 0, len(storage)-1)
			for _, j := range storage {
				if j != i {
					routes = append(routes, reserved[j].cluster)
				}
			}
		}
		s.machines = append(s.machines, prepareMachine(t, s, name, reserved[i], routes, servers))
	}
	return s
}

// storageNodes returns the indexes of the machines that store the site journal,
// mirroring the platform's own rule: one storage node for a site smaller than
// three machines, the first three by sorted name otherwise.
func storageNodes(names []string) []int {
	sorted := slices.Sorted(slices.Values(names))
	count := 1
	if len(sorted) > 2 {
		count = 3
	}
	indexes := make([]int, 0, count)
	for _, name := range sorted[:min(count, len(sorted))] {
		indexes = append(indexes, slices.Index(names, name))
	}
	return indexes
}

// machine returns one prepared machine of the site by name.
func (s *site) machine(t *testing.T, name string) *machine {
	t.Helper()
	for _, m := range s.machines {
		if m.name == name {
			return m
		}
	}
	require.FailNowf(t, "no such machine", "%s is not a machine of project %s", name, s.project)
	return nil
}

// startAll starts every machine of the site in declaration order and waits for
// each to serve.
//
// Order is not incidental. A site smaller than three machines runs JetStream on
// one deterministic storage node, and a node that only routes to it cannot reach
// a journal that is not running yet, so the storage node starts first. Machine
// order in a blueprint is the sorted name order storage selection uses.
func (s *site) startAll(ctx context.Context, t *testing.T) {
	t.Helper()
	for _, m := range s.machines {
		m.start(ctx, t)
		waitForAPI(ctx, t, m)
	}
}

// machine is one platform process under a scenario's control: prepared, and
// running once started.
type machine struct {
	// name is the deployment machine identity.
	name string
	// url is the base URL of its registration API, known from the moment it is
	// prepared, whether or not it is running.
	url string
	// sockets are the addresses and storage it was configured with. A restart
	// reuses them, which is what makes replaying its own journal possible.
	sockets sockets

	binaryPath string
	configPath string
	launchArgs []string
	output     *bytes.Buffer
	cmd        *exec.Cmd
	stopped    bool
}

// running reports whether the machine has been started.
func (m *machine) running() bool { return m.cmd != nil && !m.stopped }

// prepareMachine writes a configuration file for one built machine without
// starting it.
//
// Preparing and starting are separate so a scenario can know where a machine
// will answer before it is running. That is what lets a machine be deliberately
// offline for part of a scenario while something else is already configured to
// call it, which is the only way to observe what the platform does about an
// expected machine that is not there.
func prepareMachine(t *testing.T, s *site, name string, reserved sockets, routes, servers []string) *machine {
	t.Helper()
	binaryPath := machineBinary(s.outDir, s.project, name)
	require.FileExists(t, binaryPath)

	configPath := filepath.Join(s.workDir, "config-"+name+".toml")
	require.NoError(t, os.WriteFile(configPath, platformConfig(reserved, routes, servers), 0o644))

	return &machine{
		name:       name,
		url:        "http://" + reserved.api,
		sockets:    reserved,
		binaryPath: binaryPath,
		configPath: configPath,
		launchArgs: manifestSlot(t, binaryPath, "a").Args,
		output:     &bytes.Buffer{},
	}
}

// platformConfig renders a platform configuration that pins every socket the
// machine binds or reaches, and places its journal storage under the scenario's
// own directory.
//
// Every list is explicit, including an empty one: an omitted list would keep the
// addresses the descriptor derived, which on one host are the wrong ones.
func platformConfig(reserved sockets, routes, servers []string) []byte {
	return fmt.Appendf(nil, `address = %q
read_header_timeout = "5s"
shutdown_timeout = "10s"
instance_dir = %q
lag_bound = "30s"
[event_fabric.nats]
data_dir = %q
startup_timeout = "30s"
catch_up_timeout = "30s"
client_address = %q
cluster_address = %q
monitor_address = %q
routes = [%s]
servers = [%s]
`, reserved.api, filepath.ToSlash(reserved.instanceDir), filepath.ToSlash(reserved.dataDir),
		reserved.client, reserved.cluster, reserved.monitor, quoteList(routes), quoteList(servers))
}

// quoteList renders addresses as a TOML array body.
func quoteList(addrs []string) string {
	quoted := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		quoted = append(quoted, fmt.Sprintf("%q", addr))
	}
	return strings.Join(quoted, ", ")
}

// start runs a prepared machine and registers its cleanup.
func (m *machine) start(ctx context.Context, t *testing.T) {
	t.Helper()
	require.Nil(t, m.cmd, "%s is already started", m.name)
	args := append([]string{"-config", m.configPath}, m.launchArgs...)
	m.cmd = exec.CommandContext(ctx, m.binaryPath, args...)
	m.cmd.Stdout = m.output
	m.cmd.Stderr = m.output
	require.NoError(t, m.cmd.Start())
	m.stopped = false
	t.Cleanup(m.stop)
}

// restart force-stops the machine and starts it again on the same sockets and
// the same journal storage, which is what makes it the same node coming back
// rather than a new one.
func (m *machine) restart(ctx context.Context, t *testing.T) {
	t.Helper()
	m.stop()
	m.cmd = nil
	m.output = &bytes.Buffer{}
	m.start(ctx, t)
}

// stop force-stops the machine. A graceful child interrupt is not portable, and
// stopping hard is also the more demanding test: the journal is on disk, so a
// node that is killed must still come back to the same state. The orderly
// shutdown a signal would cause, and the order it releases dependencies in, are
// covered by the platform's in-process lifecycle tests instead.
func (m *machine) stop() {
	if m.stopped || m.cmd == nil {
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

// wait blocks until the machine's process exits and returns its output. It is
// for a scenario about a platform that is supposed to fail to start: waiting is
// the assertion, and the output is why.
func (m *machine) wait(t *testing.T) string {
	t.Helper()
	require.NotNil(t, m.cmd, "%s was never started", m.name)
	_ = m.cmd.Wait()
	m.stopped = true
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
		// Cleanup deliberately kills the child, so its output and exit error are
		// not assertions about the scenario result.
		_, _ = p.wait()
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

// freeAddresses reserves n distinct ephemeral loopback ports, then releases them
// so the machines can bind them. Holding every listener until all are reserved
// is what guarantees they are distinct; a brief race with the rest of the host
// afterwards is acceptable for a scenario.
func freeAddresses(t *testing.T, n int) []string {
	t.Helper()
	var listen net.ListenConfig
	listeners := make([]net.Listener, 0, n)
	addrs := make([]string, 0, n)
	for range n {
		listener, err := listen.Listen(t.Context(), "tcp", "127.0.0.1:0")
		require.NoError(t, err)
		listeners = append(listeners, listener)
		addrs = append(addrs, listener.Addr().String())
	}
	for _, listener := range listeners {
		require.NoError(t, listener.Close())
	}
	return addrs
}

// diagnose renders everything worth knowing when a scenario fails: what each
// machine printed, whether it was running, and where it answers.
//
// It is only ever called on a failure path. A passing run says nothing, because
// the point of a scenario that passes is that nobody has to read it.
func diagnose(machines []*machine) string {
	var b strings.Builder
	for _, m := range machines {
		state := "running"
		if !m.running() {
			state = "not started"
		}
		fmt.Fprintf(&b, "\n--- machine %s (%s, api %s, journal %s) ---\n%s",
			m.name, state, m.url, m.sockets.dataDir, m.output.String())
	}
	return b.String()
}
