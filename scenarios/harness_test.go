package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"text/template"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/utils/processtree"
	"github.com/miroslav-matejovsky/opdl/utils/testnet"
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

// machineFixture is one machine of a scenario blueprint: its identity, the
// loopback address it deploys on, and its local redundancy policy.
//
// Every fixture states the standby policy explicitly. The blueprint contract
// requires it, and a scenario that let it default would be testing a decision
// nobody made.
type machineFixture struct {
	name string
	ip   string
	// standbyDisabled opts the machine out of a second local process. Scenarios
	// about site coordination disable it so a failure is about the site rather
	// than about local redundancy; the warm standby scenario enables it.
	standbyDisabled bool
}

// projectFixtures are the blueprints scenarios build from, keyed by project.
//
// The fixture model is the single source of each machine's name, address, and
// standby policy; only the NATS ports are decided per run. Keeping the machines
// here rather than in checked-in HCL is what lets the harness reserve ports on
// the right loopback address before rendering the blueprint that names them.
var projectFixtures = map[string][]machineFixture{
	// One machine, one process. The base scenario for build, run, and restart.
	"scenario": {
		{name: "node", ip: "127.0.0.1", standbyDisabled: true},
	},
	// One machine running both local processes, for the manifest launch contract
	// and the warm standby failover scenario.
	"manifest-contract": {
		{name: "node", ip: "127.0.0.1", standbyDisabled: false},
	},
	// Two machines in one site: the smallest topology that forms a real fabric.
	// Both disable the standby so the scenario isolates site coordination from
	// local process redundancy.
	"two-machine": {
		{name: "node-a", ip: "127.0.0.1", standbyDisabled: true},
		{name: "node-b", ip: "127.0.0.2", standbyDisabled: true},
	},
	// Four machines in one site: three store the journal and route to each other,
	// and the fourth is a client of theirs. It is the topology that proves why the
	// cluster port exists and that only the selected three bind it.
	"four-machine": {
		{name: "node-a", ip: "127.0.0.1", standbyDisabled: true},
		{name: "node-b", ip: "127.0.0.2", standbyDisabled: true},
		{name: "node-c", ip: "127.0.0.3", standbyDisabled: true},
		{name: "node-d", ip: "127.0.0.4", standbyDisabled: true},
	},
}

// renderedMachine is one machine's template data: its fixture identity plus the
// ports reserved for this run.
type renderedMachine struct {
	Name            string
	IP              string
	ClientPort      int
	ClusterPort     int
	StandbyDisabled bool
}

// renderedProject is the blueprint template's data.
type renderedProject struct {
	Name     string
	Site     string
	Machines []renderedMachine
}

// natsPorts is one machine's reserved NATS ports and the addresses they resolve
// to, kept so a scenario can assert what the deployment should have derived.
type natsPorts struct {
	client  string
	cluster string
}

// stageBlueprint reserves each machine's NATS ports and renders the project's
// blueprint into a temporary blueprint root.
//
// The reservations are held, not released. They stay open through rendering and
// building so nothing else on the host can take a port while the blueprint that
// names it is being compiled into a binary. The returned release is called
// immediately before the first process that has to bind those ports starts.
func stageBlueprint(t *testing.T, project string) (root string, ports map[string]natsPorts, release func()) {
	t.Helper()
	fixtures, ok := projectFixtures[project]
	require.Truef(t, ok, "no blueprint fixture for project %q", project)

	data := renderedProject{Name: project, Site: scenarioSite}
	ports = make(map[string]natsPorts, len(fixtures))
	var reservations []*testnet.Reservation
	t.Cleanup(func() {
		for _, r := range reservations {
			_ = r.Release()
		}
	})

	for _, fixture := range fixtures {
		// Reserve on this machine's own loopback address. A port is only free per
		// interface, so reserving on 127.0.0.1 would say nothing about 127.0.0.2.
		res, err := testnet.ReserveOn(t.Context(), fixture.ip, 2)
		require.NoError(t, err)
		reservations = append(reservations, res)

		addrs := res.Addresses()
		client, cluster := addrs[0], addrs[1]
		data.Machines = append(data.Machines, renderedMachine{
			Name:            fixture.name,
			IP:              fixture.ip,
			ClientPort:      portOf(t, client),
			ClusterPort:     portOf(t, cluster),
			StandbyDisabled: fixture.standbyDisabled,
		})
		ports[fixture.name] = natsPorts{client: client, cluster: cluster}
	}

	root = filepath.Join(t.TempDir(), "blueprints")
	projectDir := filepath.Join(root, project)
	require.NoError(t, os.MkdirAll(projectDir, 0o755))

	tmpl, err := template.ParseFiles(filepath.Join("testdata", "project.hcl.tmpl"))
	require.NoError(t, err)
	var rendered bytes.Buffer
	require.NoError(t, tmpl.Execute(&rendered, data))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "project.hcl"), rendered.Bytes(), 0o644))

	// Release is idempotent, so the cleanup above remains a safety net for a
	// scenario that fails before it gets this far.
	return root, ports, func() {
		for _, r := range reservations {
			require.NoError(t, r.Release())
		}
	}
}

// portOf returns the numeric port of a host:port address.
func portOf(t *testing.T, addr string) int {
	t.Helper()
	_, port, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	number, err := strconv.Atoi(port)
	require.NoError(t, err)
	return number
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
	output, err := runCommand(build)
	require.NoError(t, err, "builder build failed:\n%s", output)
}

// runCommand runs a command to completion while keeping its whole process tree
// under scenario control. This matters on Windows, where killing a direct child
// does not kill the go build processes it started.
func runCommand(command *exec.Cmd) ([]byte, error) {
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	tree, err := processtree.Start(command)
	if err != nil {
		return output.Bytes(), err
	}
	err = errors.Join(command.Wait(), tree.Close())
	return output.Bytes(), err
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

type launch struct {
	Args []string `json:"args"`
}

type packageManifest struct {
	Primary launch  `json:"primary"`
	Standby *launch `json:"standby"`
}

func readManifest(t *testing.T, binaryPath string) packageManifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(binaryPath), "manifest.json"))
	require.NoError(t, err)
	var manifest packageManifest
	require.NoError(t, json.Unmarshal(data, &manifest))
	require.NotEmpty(t, manifest.Primary.Args)
	return manifest
}

// sockets are one machine's addresses and its local directories.
//
// Only api, dataDir, and instanceDir are runtime settings: they are the
// machine's own concerns, and a site owns them. The client and cluster addresses
// are not settings at all here. They were rendered into the blueprint before the
// build and are carried only so a scenario can assert what the deployment should
// have derived from them.
//
// There is no monitor address. The platform runs no NATS monitoring listener;
// its status files are the local operational surface.
type sockets struct {
	api         string
	dataDir     string
	instanceDir string
	eventDir    string

	// client and cluster are the addresses the blueprint was rendered with, for
	// diagnostics and assertions only. Nothing writes them to a config file.
	client  string
	cluster string
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

// deploySite renders the project's blueprint on reserved ports, builds every
// machine from it, and prepares each one to run.
//
// It replaces the older split between building and preparing because the two are
// no longer independent: the NATS ports are blueprint values now, so they must be
// chosen before the build rather than handed to the runtime after it. That is the
// point of the change. A scenario exercises the same contract a customer build
// does, instead of a runtime override path that no deployment uses.
func deploySite(ctx context.Context, t *testing.T, outDir, workDir, project string) *site {
	t.Helper()

	blueprints, ports, releasePorts := stageBlueprint(t, project)
	buildProject(ctx, t, blueprints, outDir, project)

	s := &site{project: project, outDir: outDir, workDir: workDir}
	// The API address stays a runtime setting: it is the machine's own public
	// endpoint, not site topology, so a site may move it without a rebuild.
	fixtures := projectFixtures[project]
	api, err := testnet.Reserve(t.Context(), len(fixtures))
	require.NoError(t, err)
	require.NoError(t, api.Release())
	apiAddrs := api.Addresses()

	for i, fixture := range fixtures {
		s.machines = append(s.machines, prepareMachine(t, s, fixture.name, sockets{
			api:         apiAddrs[i],
			dataDir:     filepath.Join(workDir, "nats-"+fixture.name),
			instanceDir: filepath.Join(workDir, "instance-"+fixture.name),
			eventDir:    filepath.Join(workDir, "operations-"+fixture.name),
			client:      ports[fixture.name].client,
			cluster:     ports[fixture.name].cluster,
		}))
	}

	// The blueprint is built, so the ports it named are now the deployment's.
	// Releasing them here is what lets the first machine bind them.
	releasePorts()
	return s
}

// storageMachines returns the names of the machines that store the site journal,
// mirroring the platform's own rule: one storage machine for a site smaller than
// three machines, the first three by sorted name otherwise.
//
// A scenario derives this the same way the platform does rather than being told,
// so it cannot quietly disagree with the deployment about who stores what.
func storageMachines(names []string) []string {
	sorted := slices.Sorted(slices.Values(names))
	count := 1
	if len(sorted) > 2 {
		count = 3
	}
	return sorted[:min(count, len(sorted))]
}

// machineNames returns the names of a project's fixture machines.
func machineNames(project string) []string {
	fixtures := projectFixtures[project]
	names := make([]string, 0, len(fixtures))
	for _, fixture := range fixtures {
		names = append(names, fixture.name)
	}
	return names
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

// startTogether starts the named machines at once and only then waits for each
// to serve.
//
// A site with three storage machines cannot be started one at a time. Their
// journal's metadata group needs a quorum of the three servers before it can
// create the stream, so the first machine cannot finish starting until the other
// two are already running. Waiting for each in turn would deadlock on the first.
func (s *site) startTogether(ctx context.Context, t *testing.T, names ...string) {
	t.Helper()
	started := make([]*machine, 0, len(names))
	for _, name := range names {
		m := s.machine(t, name)
		m.start(ctx, t)
		started = append(started, m)
	}
	for _, m := range started {
		waitForAPI(ctx, t, m)
	}
}

// syncBuffer is a process output buffer that is safe to read while the process
// is still writing to it.
//
// exec copies a child's stdout and stderr on goroutines of its own, so every
// diagnostic that reads a running machine's output races that copier. A plain
// bytes.Buffer makes that a genuine data race, which under -race fails the
// scenario for a reason that has nothing to do with the platform.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// machine is one platform process under a scenario's control: prepared, and
// running once started.
type machine struct {
	// project is part of the compiled deployment identity and local status path.
	project string
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
	output     *syncBuffer
	cmd        *exec.Cmd
	tree       *processtree.Owner
	// done closes once the process has exited and err is set. It is what lets
	// running report what the process is doing rather than only what the scenario
	// last asked it to do.
	done    chan struct{}
	err     error
	stopped bool
}

// processStatus is the local operational contract deployment tooling reads.
// It is re-declared here so scenarios consume the packaged runtime as a black
// box instead of importing platform internals.
type processStatus struct {
	Role       string    `json:"role"`
	State      string    `json:"state"`
	PID        int       `json:"pid"`
	Applied    uint64    `json:"applied"`
	HighWater  uint64    `json:"high_water"`
	Promotable bool      `json:"promotable"`
	UpdatedAt  time.Time `json:"updated_at"`
	LastError  string    `json:"last_error"`
}

// managedProcess is one explicitly named primary or standby process. It is used
// by redundancy scenarios that need to stop one process without stopping the
// other process of the same machine.
type managedProcess struct {
	role   string
	output *syncBuffer
	cmd    *exec.Cmd
	tree   *processtree.Owner
	done   chan struct{}
	err    error
}

// running reports whether the machine's process is still alive.
//
// It observes the process rather than the scenario's own bookkeeping. A machine
// that shut itself down is exactly what this has to be able to report: the
// platform stops serving when its event fabric stops carrying events, and a
// machine that had exited on its own still looked started here, so an assertion
// that it kept serving could never fail.
func (m *machine) running() bool {
	if m.cmd == nil || m.stopped {
		return false
	}
	select {
	case <-m.done:
		return false
	default:
		return true
	}
}

func (m *machine) startManaged(ctx context.Context, t *testing.T, role string, args []string) *managedProcess {
	t.Helper()
	p := &managedProcess{
		role:   role,
		output: &syncBuffer{},
		done:   make(chan struct{}),
	}
	commandArgs := append([]string{"-config", m.configPath}, args...)
	p.cmd = exec.CommandContext(ctx, m.binaryPath, commandArgs...)
	processtree.ConfigureGraceful(p.cmd)
	p.cmd.Stdout = p.output
	p.cmd.Stderr = p.output
	var err error
	p.tree, err = processtree.Start(p.cmd)
	require.NoError(t, err)
	go func() {
		p.err = errors.Join(p.cmd.Wait(), p.tree.Close())
		close(p.done)
	}()
	t.Cleanup(p.kill)
	return p
}

func (p *managedProcess) pid() int { return p.cmd.Process.Pid }

func (p *managedProcess) running() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (p *managedProcess) kill() {
	if !p.running() {
		return
	}
	_ = p.tree.Kill()
	<-p.done
}

func (p *managedProcess) stopGracefully(t *testing.T) {
	t.Helper()
	if !p.running() {
		return
	}
	require.NoError(t, p.tree.Stop())
	select {
	case <-p.done:
		require.NoError(t, p.err, "%s did not stop cleanly:\n%s", p.role, p.output.String())
	case <-time.After(apiWaitTimeout):
		p.kill()
		require.FailNowf(t, p.role+" did not stop", "%s", p.output.String())
	}
}

func (p *managedProcess) logs() string { return p.output.String() }

// exitResult returns the complete output and exit error of a process that has
// already finished. Waiting on done joins the exec copier goroutines, so the
// buffer is only complete once it has closed.
func (p *managedProcess) exitResult() (string, error) {
	<-p.done
	return p.output.String(), p.err
}

func (m *machine) statusPath(role string) string {
	machineDir := strings.Join([]string{m.project, "development", scenarioSite, m.name}, "-")
	return filepath.Join(m.sockets.instanceDir, machineDir, "process-"+role+".status")
}

func (m *machine) readStatus(role string) (processStatus, error) {
	data, err := os.ReadFile(m.statusPath(role))
	if err != nil {
		return processStatus{}, err
	}
	var status processStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return processStatus{}, err
	}
	return status, nil
}

// waitStatus blocks until a process reports the expected lifecycle state.
//
// The bound is not weakened for a slow standby, and a state reached with a
// non-empty LastError is not accepted. A standby that reports StateStandby while
// still failing to reach the journal is exactly the failure this scenario exists
// to catch, so treating it as success would make the scenario pass for the wrong
// reason.
//
// A connection error while the process is alive is a retryable "not yet" and
// polling continues. A process that has exited will never reach any state, so
// that fails immediately with its output rather than burning the whole timeout.
func (m *machine) waitStatus(t *testing.T, process *managedProcess, state string, promotable bool) processStatus {
	t.Helper()
	var last processStatus
	var readErr error
	deadline := time.Now().Add(apiWaitTimeout)
	for {
		if !process.running() {
			output, _ := process.exitResult()
			require.FailNowf(t, process.role+" exited before reaching "+state,
				"%s", statusDiagnostics(m, process, state, last, readErr, output))
		}
		status, err := m.readStatus(process.role)
		readErr = err
		if err == nil {
			last = status
			if status.Role == process.role && status.PID == process.pid() && status.State == state &&
				(!promotable || status.Promotable) && status.LastError == "" {
				return last
			}
		}
		if time.Now().After(deadline) {
			require.FailNowf(t, process.role+" never reached "+state,
				"%s", statusDiagnostics(m, process, state, last, readErr, process.logs()))
		}
		time.Sleep(apiPollInterval)
	}
}

// statusDiagnostics renders why a process never reached a state: what it last
// reported, whether a status file existed at all, the endpoints it composed, and
// its output.
//
// The distinction between a missing status file and a status that reports an
// unreachable journal is the one worth preserving. The first means the process
// never got far enough to write one; the second means it is running and cannot
// reach the address it was told to use, which is a topology problem rather than a
// startup one.
func statusDiagnostics(m *machine, process *managedProcess, want string, last processStatus, readErr error, output string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "machine %s process %s wanted state %q\n", m.name, process.role, want)
	fmt.Fprintf(&b, "status file: %s\n", m.statusPath(process.role))
	switch {
	case readErr != nil && os.IsNotExist(readErr):
		b.WriteString("status: never written\n")
	case readErr != nil:
		fmt.Fprintf(&b, "status: unreadable: %v\n", readErr)
	default:
		fmt.Fprintf(&b, "status: %+v\n", last)
		if last.LastError != "" {
			fmt.Fprintf(&b, "status last error: %s\n", last.LastError)
		}
	}
	fmt.Fprintf(&b, "expected event fabric endpoints: client=%s cluster=%s\n",
		m.sockets.client, m.sockets.cluster)
	fmt.Fprintf(&b, "logs:\n%s", output)
	fmt.Fprintf(&b, "\noperational events:\n%s", operationEvents(m))
	return b.String()
}

// operationEvents reads every retained JSONL stream for a machine. A restart or
// warm standby creates another PID-specific file, so diagnostics include all of
// them in filename order rather than guessing which process matters.
func operationEvents(m *machine) string {
	paths, err := filepath.Glob(filepath.Join(m.sockets.eventDir, "*.jsonl"))
	if err != nil {
		return fmt.Sprintf("glob %s: %v\n", m.sockets.eventDir, err)
	}
	if len(paths) == 0 {
		return "(none)\n"
	}
	var b strings.Builder
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(&b, "--- %s: %v ---\n", path, err)
			continue
		}
		fmt.Fprintf(&b, "--- %s ---\n%s", filepath.Base(path), data)
	}
	return b.String()
}

// prepareMachine writes a configuration file for one built machine without
// starting it.
//
// Preparing and starting are separate so a scenario can know where a machine
// will answer before it is running. That is what lets a machine be deliberately
// offline for part of a scenario while something else is already configured to
// call it, which is the only way to observe what the platform does about an
// expected machine that is not there.
func prepareMachine(t *testing.T, s *site, name string, reserved sockets) *machine {
	t.Helper()
	binaryPath := machineBinary(s.outDir, s.project, name)
	require.FileExists(t, binaryPath)

	configPath := filepath.Join(s.workDir, "config-"+name+".toml")
	require.NoError(t, os.WriteFile(configPath, platformConfig(reserved), 0o644))

	return &machine{
		project:    s.project,
		name:       name,
		url:        "http://" + reserved.api,
		sockets:    reserved,
		binaryPath: binaryPath,
		configPath: configPath,
		launchArgs: readManifest(t, binaryPath).Primary.Args,
		output:     &syncBuffer{},
	}
}

// platformConfig renders a platform configuration holding only what a site
// owns: where this machine answers, where its journal and coordination state
// live, and its timeouts.
//
// It carries no NATS socket topology. Those addresses came from the blueprint
// and are compiled into the machine's descriptor, and the runtime now rejects a
// configuration file that sets them. That rejection is the point: a scenario
// that could still override them would be exercising a path no deployment has,
// which is what let the warm standby defect stay hidden.
func platformConfig(reserved sockets) []byte {
	return fmt.Appendf(nil, `address = %q
read_header_timeout = "5s"
shutdown_timeout = "10s"
instance_dir = %q
lag_bound = "30s"
[operations]
event_dir = %q
[event_fabric.nats]
data_dir = %q
startup_timeout = "30s"
catch_up_timeout = "30s"
`, reserved.api, filepath.ToSlash(reserved.instanceDir), filepath.ToSlash(reserved.eventDir), filepath.ToSlash(reserved.dataDir))
}

// start runs a prepared machine and registers its cleanup.
func (m *machine) start(ctx context.Context, t *testing.T) {
	t.Helper()
	require.Nil(t, m.cmd, "%s is already started", m.name)
	args := append([]string{"-config", m.configPath}, m.launchArgs...)
	m.cmd = exec.CommandContext(ctx, m.binaryPath, args...)
	m.cmd.Stdout = m.output
	m.cmd.Stderr = m.output
	var err error
	m.tree, err = processtree.Start(m.cmd)
	require.NoError(t, err)
	m.stopped = false
	m.done = make(chan struct{})
	go func() {
		m.err = errors.Join(m.cmd.Wait(), m.tree.Close())
		close(m.done)
	}()
	t.Cleanup(m.stop)
}

// restart force-stops the machine and starts it again on the same sockets and
// the same journal storage, which is what makes it the same node coming back
// rather than a new one.
func (m *machine) restart(ctx context.Context, t *testing.T) {
	t.Helper()
	m.stop()
	m.cmd = nil
	m.output = &syncBuffer{}
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
	_ = m.tree.Kill()
	<-m.done
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
	<-m.done
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
	output *syncBuffer
	tree   *processtree.Owner
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
	p := &process{output: &syncBuffer{}, finished: make(chan struct{})}
	cmd.Stdout = p.output
	cmd.Stderr = p.output
	var err error
	p.tree, err = processtree.Start(cmd)
	require.NoError(t, err)
	go func() {
		p.err = errors.Join(cmd.Wait(), p.tree.Close())
		close(p.finished)
	}()
	t.Cleanup(func() {
		// Whatever went wrong, the child does not outlive the scenario, and the
		// scenario does not return while it is still writing to the buffer.
		_ = p.tree.Kill()
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
		fmt.Fprintf(&b, "\n--- machine %s operational events ---\n%s", m.name, operationEvents(m))
	}
	return b.String()
}

// lazily defers rendering part of a failure message until an assertion actually
// fails.
//
// A message argument is evaluated where it is written, not where it is
// formatted, so passing diagnose(...) straight into a wait captured every
// machine's output before the wait had had a chance to fail. The logs that
// explained the failure were then precisely the ones missing from it, because
// they had not been written yet. testify formats a message only on failure, so a
// Stringer is rendered at the moment worth describing.
type lazily func() string

func (l lazily) String() string { return l() }

// diagnostics renders diagnose for these machines, at failure time.
func diagnostics(machines ...*machine) fmt.Stringer {
	return lazily(func() string { return diagnose(machines) })
}

// appended renders trailing failure context, and nothing at all when a caller
// passed none.
func appended(parts []fmt.Stringer) fmt.Stringer {
	return lazily(func() string {
		var b strings.Builder
		for _, part := range parts {
			b.WriteString(part.String())
		}
		return b.String()
	})
}
