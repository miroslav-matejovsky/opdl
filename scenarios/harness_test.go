package scenarios

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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

	"github.com/miroslav-matejovsky/opdl/utils/procrun"
	"github.com/miroslav-matejovsky/opdl/utils/semaphore"
	"github.com/miroslav-matejovsky/opdl/utils/testnet"
	"github.com/miroslav-matejovsky/opdl/utils/waitfor"
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

var (
	// budget bounds the total number of platform processes the suite runs at once.
	budget = semaphore.New(max(2, runtime.GOMAXPROCS(0)/2))
	// buildBudget bounds concurrent builder compilations / executions to 2.
	buildBudget = semaphore.New(2)
	// builderBinary holds the path to the precompiled builder CLI executable.
	builderBinary string
)

func runMain(m *testing.M) int {
	flag.Parse()
	if testing.Short() {
		fmt.Println("skipping scenario suite in -short mode")
		return 0
	}

	// Compile builder once for all scenarios to eliminate repeated go run compilations
	// and build-cache contention during parallel runs.
	tmpDir, err := os.MkdirTemp("", "opdl-builder-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir for builder binary: %v\n", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	builderBinary = filepath.Join(tmpDir, "opdl.exe")

	builderDir, err := filepath.Abs(filepath.Join("..", "builder"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to locate builder dir: %v\n", err)
		return 1
	}

	buildCmd := exec.CommandContext(context.Background(), "go", "build", "-o", builderBinary, "./cmd/opdl")
	buildCmd.Dir = builderDir
	if out, err := buildCmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to compile builder CLI: %v\n%s\n", err, out)
		return 1
	}

	return m.Run()
}

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
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
	Name string
	IP   string
	// The Primary Instance's ports and its own runtime directory.
	APIPort     int
	RuntimeDir  string
	DataDir     string
	ClientPort  int
	ClusterPort int
	// The Standby Instance's, empty or zero when the machine deploys none. The
	// two instances run together on one host, so every one of these is its own
	// listener or directory and none may repeat.
	StandbyAPIPort     int
	StandbyRuntimeDir  string
	StandbyDataDir     string
	StandbyClientPort  int
	StandbyClusterPort int
	StandbyDisabled    bool
}

// renderedProject is the blueprint template's data.
type renderedProject struct {
	Name string
	Site string
	// RunToken isolates this run's machine locks and service names from every other run's.
	// See runToken.
	RunToken string
	Machines []renderedMachine
}

// runToken returns a unique token no other build shares.
//
// A machine lock is a kernel object in a machine-wide namespace. Several scenarios deliberately build
// the same project and machine names, and they run in parallel, so without this
// they would contend for one another's ownership and a standby in one test would
// wait on a primary in another.
//
// The token is random rather than derived from the test name so that two
// concurrent runs of the whole suite on one host also stay isolated.
func runToken(t *testing.T) string {
	t.Helper()
	token := make([]byte, 8)
	_, err := rand.Read(token)
	require.NoError(t, err)
	return "opdl-scenario-" + hex.EncodeToString(token)
}

// reservedEndpoints is one machine's reserved ports and directories, kept so a
// scenario can assert what the deployment should have derived from the blueprint
// it was rendered into.
type reservedEndpoints struct {
	// apiPort is the Primary Instance's loopback API port. The builder joins it
	// with 127.0.0.1, never with the machine's ip, so these are reserved on
	// 127.0.0.1 for every machine however many loopback addresses the site uses.
	apiPort int
	client  int
	cluster int
}

var (
	scenarioDirsMu sync.Mutex
	scenarioDirs   = make(map[string]*sync.Once)
)

// scenarioDir returns this scenario's scratch root under scenarios/.tmp,
// emptied on first use in this run.
//
// Layout:
//
//	scenarios/.tmp/
//	  TestFourMachineStorageTopologyAndFailure/
//	    blueprints/    rendered project.hcl
//	    out/           built machine packages and manifests
//	    work/          config-*.toml, nats-*, instance-*, operations-*
//	    control/       marker files
func scenarioDir(t *testing.T) string {
	t.Helper()
	testName, _, _ := strings.Cut(t.Name(), "/")

	scenarioDirsMu.Lock()
	once, ok := scenarioDirs[testName]
	if !ok {
		once = new(sync.Once)
		scenarioDirs[testName] = once
	}
	scenarioDirsMu.Unlock()

	baseDir := os.Getenv("OPDL_SCENARIO_TMP")
	if baseDir == "" {
		baseDir = ".tmp"
	}
	dir, err := filepath.Abs(filepath.Join(baseDir, testName))
	require.NoError(t, err)

	once.Do(func() {
		if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
			t.Fatalf("failed to empty scratch directory %s (a stale process from a previous run may still be holding a file): %v", dir, err)
		}
		for _, sub := range []string{"blueprints", "out", "work", "control"} {
			require.NoError(t, os.MkdirAll(filepath.Join(dir, sub), 0o755))
		}
	})

	return dir
}

// apiPortsWanted counts the API ports a project's instances need: one per
// deployed instance.
func apiPortsWanted(fixtures []machineFixture) int {
	wanted := 0
	for _, fixture := range fixtures {
		wanted++
		if !fixture.standbyDisabled {
			wanted++
		}
	}
	return wanted
}

// runtimeDirFor is one instance's own local runtime directory.
//
// It is per instance and per machine because several machines share this host, so
// the isolation the deployed shape gets from two hosts has to be rendered in
// here, exactly as the per-run mutex name is.
func runtimeDirFor(workDir, machine, role string) string {
	return filepath.ToSlash(filepath.Join(workDir, "instance-"+machine, role))
}

// dataDirFor is one instance's own JetStream file store directory.
//
// It is per instance for a stricter reason than runtimeDirFor's. Two instances
// sharing a runtime directory overwrite each other's status file silently; two
// NATS servers cannot open one JetStream store at all, and on this host every
// deployed instance of a storage machine runs a server. It is kept out of the
// instance directory so a scenario reading one instance's evidence is not
// walking a journal to find it.
func dataDirFor(workDir, machine, role string) string {
	return filepath.ToSlash(filepath.Join(workDir, "journal-"+machine, role))
}

// stageBlueprint allocates each machine's ports from the testnet pool, names each
// instance's runtime directory, and renders the project's blueprint into a
// temporary blueprint root.
func stageBlueprint(t *testing.T, project, workDir string) (root string, endpoints map[string]reservedEndpoints) {
	t.Helper()
	fixtures, ok := projectFixtures[project]
	require.Truef(t, ok, "no blueprint fixture for project %q", project)

	data := renderedProject{Name: project, Site: scenarioSite, RunToken: runToken(t)}
	endpoints = make(map[string]reservedEndpoints, len(fixtures))

	// Every instance's API port comes from one reservation on 127.0.0.1, because
	// that is the only interface any of them is resolved onto. Reserving them per
	// machine ip the way the NATS ports are would say nothing about whether two
	// machines had been given the same loopback port, and the collision would only
	// appear as the second machine failing to bind.
	apiPorts, err := testnet.Take("127.0.0.1", apiPortsWanted(fixtures))
	require.NoError(t, err)
	nextAPIPort := 0
	takeAPIPort := func() int {
		port := apiPorts[nextAPIPort]
		nextAPIPort++
		return port
	}

	for _, fixture := range fixtures {
		// Take the Event Fabric's ports from this machine's own loopback address.
		// A port is only free per interface, so taking on 127.0.0.1 would say
		// nothing about 127.0.0.2.
		//
		// A machine that deploys both instances needs four: each instance runs its
		// own Event Fabric server. Reserving them in one call is what keeps them
		// distinct, which the builder requires.
		wanted := 2
		if !fixture.standbyDisabled {
			wanted = 4
		}
		p, err := testnet.Take(fixture.ip, wanted)
		require.NoError(t, err)

		machine := renderedMachine{
			Name:            fixture.name,
			IP:              fixture.ip,
			APIPort:         takeAPIPort(),
			RuntimeDir:      runtimeDirFor(workDir, fixture.name, "primary"),
			DataDir:         dataDirFor(workDir, fixture.name, "primary"),
			ClientPort:      p[0],
			ClusterPort:     p[1],
			StandbyDisabled: fixture.standbyDisabled,
		}
		if !fixture.standbyDisabled {
			machine.StandbyAPIPort = takeAPIPort()
			machine.StandbyRuntimeDir = runtimeDirFor(workDir, fixture.name, "standby")
			machine.StandbyDataDir = dataDirFor(workDir, fixture.name, "standby")
			machine.StandbyClientPort = p[2]
			machine.StandbyClusterPort = p[3]
		}
		data.Machines = append(data.Machines, machine)
		endpoints[fixture.name] = reservedEndpoints{
			apiPort: machine.APIPort,
			client:  machine.ClientPort,
			cluster: machine.ClusterPort,
		}
	}

	root = filepath.Join(scenarioDir(t), "blueprints")
	projectDir := filepath.Join(root, project)
	require.NoError(t, os.MkdirAll(projectDir, 0o755))

	tmpl, err := template.ParseFiles(filepath.Join("testdata", "project.hcl.tmpl"))
	require.NoError(t, err)
	var rendered bytes.Buffer
	require.NoError(t, tmpl.Execute(&rendered, data))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "project.hcl"), rendered.Bytes(), 0o644))

	return root, endpoints
}

// buildProject drives the builder CLI to build every machine of a blueprint into
// outDir. It is the same command a customer runs. Note that cmd.Dir must be set
// explicitly rather than calling os.Chdir anywhere in the harness, because a chdir
// inside a process with parallel tests would break all concurrent paths.
func buildProject(ctx context.Context, t *testing.T, blueprintsDir, outDir, project string) {
	t.Helper()
	require.NoError(t, buildBudget.Acquire(ctx, 1))
	defer buildBudget.Release(1)

	build := exec.CommandContext(ctx, builderBinary, "build",
		"-examples", blueprintsDir, "-out", outDir, project)
	proc, err := procrun.Start(build)
	require.NoError(t, err)
	output, err := proc.Wait()
	require.NoError(t, err, "builder build failed:\n%s", output)
}

// machineBinary returns the path of one built machine's binary. The builder
// names each package after the machine it is for.
func machineBinary(outDir, project, machine string) string {
	return filepath.Join(outDir, project, scenarioSite, machine, machine+".exe")
}

type winService struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

type launch struct {
	Service winService `json:"service"`
	Args    []string   `json:"args"`
}

type packageManifest struct {
	MachineProfile string  `json:"machine_profile"`
	Primary        launch  `json:"primary"`
	Standby        *launch `json:"standby"`
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
// Only eventDir is a runtime setting now: it is the machine's own concern and a
// site owns it. Everything else was rendered into the blueprint before the build
// and is carried only so a scenario can reach a machine and assert what the
// deployment should have derived. dataDir is in that second group since stage
// 05: it is the Primary Instance's authored store, repeated here so a scenario
// can find the directory rather than state it.
//
// The API address moved into that second group in stage 04, along with each
// instance's runtime directory. Both are an instance's rather than a machine's,
// so the descriptor resolves them per instance and the configuration file cannot
// state either. The runtime directories are not carried here at all: they are
// derived per role by machine.statusPath.
//
// There is no monitor address. The platform runs no NATS monitoring listener;
// its status files are the local operational surface.
type sockets struct {
	dataDir  string
	eventDir string

	// api, client, and cluster are the blueprint's, for reaching a machine and
	// for assertions. Nothing writes them to a config file.
	api     string
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

// deploySite renders the project's blueprint with allocated ports, builds every
// machine from it, and prepares each one to run.
//
// It replaces the older split between building and preparing because the two are
// no longer independent: the NATS ports are blueprint values now, so they must be
// chosen before the build rather than handed to the runtime after it. That is the
// point of the change. A scenario exercises the same contract a customer build
// does, instead of a runtime override path that no deployment uses.
func deploySite(ctx context.Context, t *testing.T, outDir, workDir, project string) *site {
	t.Helper()

	fixtures := projectFixtures[project]
	nMachines := len(fixtures)
	require.NoError(t, budget.Acquire(ctx, nMachines))
	t.Cleanup(func() {
		budget.Release(nMachines)
	})

	blueprints, endpoints := stageBlueprint(t, project, workDir)
	buildProject(ctx, t, blueprints, outDir, project)

	s := &site{project: project, outDir: outDir, workDir: workDir}
	for _, fixture := range fixtures {
		reserved := endpoints[fixture.name]
		s.machines = append(s.machines, prepareMachine(t, s, fixture.name, sockets{
			// The Primary Instance's own journal store, matching what the
			// blueprint authored for it. A scenario that sabotages storage has to
			// aim at the directory that instance actually opens, and since stage
			// 05 that is per instance rather than per machine.
			dataDir:  filepath.FromSlash(dataDirFor(workDir, fixture.name, "primary")),
			eventDir: filepath.Join(workDir, "operations-"+fixture.name),
			// The API address the builder resolved: this machine's authored
			// local_port on 127.0.0.1. A scenario reaches a machine here rather
			// than at an address it chose, because it no longer chooses one.
			api:     net.JoinHostPort("127.0.0.1", strconv.Itoa(reserved.apiPort)),
			client:  net.JoinHostPort(fixture.ip, strconv.Itoa(reserved.client)),
			cluster: net.JoinHostPort(fixture.ip, strconv.Itoa(reserved.cluster)),
		}))
	}

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

// machine is one platform process under a scenario's control: prepared, and
// running once started.
type machine struct {
	*procrun.Process
	// project is part of the compiled deployment identity and local status path.
	project string
	// name is the deployment machine identity.
	name string
	// workDir is the site's scratch space, and the root the blueprint's runtime
	// directories were rendered under. It is how a scenario finds either
	// instance's status file; see statusPath.
	workDir string
	// url is the base URL of its registration API, known from the moment it is
	// prepared, whether or not it is running.
	url string
	// sockets are the addresses and storage it was configured with. A restart
	// reuses them, which is what makes replaying its own journal possible.
	sockets sockets

	binaryPath string
	configPath string
	launchArgs []string
	stopped    bool
}

// processStatus is the local operational contract deployment tooling reads.
// It is re-declared here so scenarios consume the packaged runtime as a black
// box instead of importing platform internals.
type processStatus struct {
	Role          string    `json:"role"`
	State         string    `json:"state"`
	PID           int       `json:"pid"`
	Applied       uint64    `json:"applied"`
	HighWater     uint64    `json:"high_water"`
	FailoverReady bool      `json:"failover_ready"`
	UpdatedAt     time.Time `json:"updated_at"`
	LastError     string    `json:"last_error"`
}

// managedProcess is one explicitly named primary or standby process. It is used
// by redundancy scenarios that need to stop one process without stopping the
// other process of the same machine.
type managedProcess struct {
	*procrun.Process
	role string
}

// running reports whether the machine's process is still alive.
func (m *machine) running() bool {
	if m.Process == nil || m.stopped {
		return false
	}
	return m.Running()
}

// exited reports that this machine had a process of its own and that process is
// gone. It is the fail-fast signal for a wait: there is no point polling a
// machine that has died.
//
// It is deliberately not the negation of running. A machine whose processes are
// launched with startManaged, as the warm standby scenario does, never has a
// process of its own, so running is false for it from the start. Aborting on
// that would fail every wait against such a machine before the first poll, while
// the primary and standby serving its endpoint are perfectly healthy.
func (m *machine) exited() bool {
	return m.Process != nil && !m.running()
}

func (m *machine) startManaged(ctx context.Context, t *testing.T, role string, args []string) *managedProcess {
	t.Helper()
	commandArgs := append([]string{"-config", m.configPath}, args...)
	cmd := exec.CommandContext(ctx, m.binaryPath, commandArgs...)
	proc, err := procrun.Start(cmd)
	require.NoError(t, err)
	p := &managedProcess{
		Process: proc,
		role:    role,
	}
	t.Cleanup(func() {
		_ = p.Kill()
	})
	return p
}

func (p *managedProcess) stopGracefully(t *testing.T) {
	t.Helper()
	if !p.Running() {
		return
	}
	require.NoError(t, p.Stop())
	err := waitfor.Poll(t.Context(), apiWaitTimeout, 50*time.Millisecond, func() bool {
		return !p.Running()
	}, nil)
	if err != nil {
		_ = p.Kill()
		require.FailNowf(t, p.role+" did not stop", "%s", p.Logs())
	}
	_, exitErr := p.Wait()
	require.NoError(t, exitErr, "%s did not stop cleanly:\n%s", p.role, p.Logs())
}

// statusPath is where one of this machine's instances writes its status file.
//
// It composes nothing the runtime does not: each instance's runtime directory was
// authored in the blueprint by runtimeDirFor, and the runtime writes
// process.status inside the directory it was given. The project, site, and role
// qualifiers this path used to carry now live in the authored directory itself.
func (m *machine) statusPath(role string) string {
	return filepath.Join(runtimeDirFor(m.workDir, m.name, role), "process.status")
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

type diagStringer func() string

func (d diagStringer) String() string { return d() }

func waitFor(t *testing.T, what string, timeout, interval time.Duration, cond func() bool, abort waitfor.Abort, diag fmt.Stringer) {
	t.Helper()
	err := waitfor.Poll(t.Context(), timeout, interval, cond, abort)
	if err != nil {
		var abortErr *waitfor.AbortError
		if errors.As(err, &abortErr) {
			require.FailNowf(t, what+" aborted: "+abortErr.Reason, "%s", diag.String())
		}
		require.FailNowf(t, what+" timed out after "+timeout.String(), "%s", diag.String())
	}
}

// waitStatus blocks until a process reports the expected lifecycle state.
func (m *machine) waitStatus(t *testing.T, process *managedProcess, state string, failoverReady bool) processStatus {
	t.Helper()
	var last processStatus
	var readErr error

	cond := func() bool {
		status, err := m.readStatus(process.role)
		readErr = err
		if err == nil {
			last = status
			if status.Role == process.role && status.PID == process.PID() && status.State == state &&
				(!failoverReady || status.FailoverReady) && status.LastError == "" {
				return true
			}
		}
		return false
	}
	abort := func() (bool, string) {
		if !process.Running() {
			out, _ := process.Wait()
			return true, fmt.Sprintf("%s exited before reaching %s:\n%s", process.role, state, out)
		}
		return false, ""
	}
	// Logs, not Wait. This renders on timeout, which is precisely the case where
	// the process is still running, so Wait would block until the whole test
	// binary times out and the diagnostics would never be printed at all. Logs is
	// a snapshot of the same buffer and does not block.
	diag := diagStringer(func() string {
		return statusDiagnostics(m, process, state, last, readErr, process.Logs())
	})

	waitFor(t, "process "+process.role+" reaching state "+state, apiWaitTimeout, markerPollInterval, cond, abort, diag)
	return last
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

	m := &machine{
		project:    s.project,
		name:       name,
		workDir:    s.workDir,
		url:        "http://" + reserved.api,
		sockets:    reserved,
		binaryPath: binaryPath,
		configPath: configPath,
		launchArgs: readManifest(t, binaryPath).Primary.Args,
	}
	t.Cleanup(m.stop)
	return m
}

// platformConfig renders a platform configuration holding only what a site
// owns: where this machine's journal and operational events live, and its
// timeouts.
//
// It carries no API address and no runtime directory. Both are an instance's
// own, both were authored in the blueprint and resolved onto that instance's
// descriptor record, and the runtime rejects a configuration file that sets
// either. A machine's two instances read this one file, so anything an instance
// binds or writes that appeared here would be a value they would both take.
//
// The three tolerances are deliberately looser than platform/config.toml's 30s.
// They bound how long a machine puts up with a slow environment before giving
// up, and a parallel scenario suite is a slow environment on purpose: several
// sites start their JetStream clusters at once on one contended host. A
// four-machine site that loses a storage node has to re-form its metadata group
// under that load, and with a 30s lag_bound the surviving machines stop serving
// mid-scenario, which is the platform behaving correctly about a condition the
// harness created. No scenario asserts on these values, so raising them removes
// a false failure without weakening anything: a machine that genuinely never
// catches up still fails, on the assertion that was actually being made.
//
// It carries no NATS socket topology and, since stage 05, no data directory
// either. Those addresses came from the blueprint and are compiled into the
// machine's descriptor, and the runtime now rejects a configuration file that
// sets them. That rejection is the point: a scenario that could still override
// them would be exercising a path no deployment has, which is what let the warm
// standby defect stay hidden.
//
// The journal's storage joined them when each instance gained its own server. A
// machine's two instances open two stores, so one machine-level data_dir could
// not name both, and it is now authored per instance in the blueprint.
func platformConfig(reserved sockets) []byte {
	return fmt.Appendf(nil, `read_header_timeout = "5s"
shutdown_timeout = "10s"
lag_bound = "2m"
[operations]
event_dir = %q
[event_fabric.nats]
startup_timeout = "60s"
catch_up_timeout = "60s"
`, filepath.ToSlash(reserved.eventDir))
}

// start runs a prepared machine.
func (m *machine) start(ctx context.Context, t *testing.T) {
	t.Helper()
	require.Nil(t, m.Process, "%s is already started", m.name)
	args := append([]string{"-config", m.configPath}, m.launchArgs...)
	cmd := exec.CommandContext(ctx, m.binaryPath, args...)
	proc, err := procrun.Start(cmd)
	require.NoError(t, err)
	m.Process = proc
	m.stopped = false
}

// restart force-stops the machine and starts it again on the same sockets and
// the same journal storage, which is what makes it the same node coming back
// rather than a new one.
func (m *machine) restart(ctx context.Context, t *testing.T) {
	t.Helper()
	m.stop()
	m.Process = nil
	m.start(ctx, t)
}

// stop force-stops the machine. A graceful child interrupt is not portable, and
// stopping hard is also the more demanding test: the journal is on disk, so a
// node that is killed must still come back to the same state.
func (m *machine) stop() {
	if m.stopped || m.Process == nil {
		return
	}
	m.stopped = true
	_ = m.Kill()
}

// logs returns the machine's captured output, and says so when the machine was
// never started rather than dereferencing a process that does not exist.
//
// It does not stop the machine. The output buffer is mutex guarded, so reading
// it while the process is still writing is safe and is what diagnostics need:
// stopping a machine in order to find out what it said would destroy the state
// the failure is about. Use wait when a scenario means to observe an exit.
func (m *machine) logs() string {
	if m.Process == nil {
		return "(never started)\n"
	}
	return m.Logs()
}

// wait blocks until the machine's process exits and returns its output. It is
// for a scenario about a platform that is supposed to fail to start: waiting is
// the assertion, and the output is why.
func (m *machine) wait(t *testing.T) string {
	t.Helper()
	require.NotNil(t, m.Process, "%s was never started", m.name)
	out, _ := m.Wait()
	m.stopped = true
	return out
}

// waitForMarker blocks until name appears in dir, which is how a scenario waits
// for something inside another process to reach a point.
func waitForMarker(t *testing.T, dir, name string, signaller *procrun.Process, describe func() string) {
	t.Helper()
	path := filepath.Join(dir, name)
	cond := func() bool {
		_, err := os.Stat(path)
		return err == nil
	}
	abort := func() (bool, string) {
		if !signaller.Running() {
			output, err := signaller.Wait()
			return true, fmt.Sprintf("the process that signals %s exited before writing it (exit: %v):\n%s", name, err, output)
		}
		return false, ""
	}
	diag := diagStringer(describe)
	waitFor(t, name+" marker", markerWaitTimeout, markerPollInterval, cond, abort, diag)
}

// diagnose renders everything worth knowing when a scenario fails: what each
// machine printed, whether it was running, and where it answers.
func diagnose(machines []*machine) string {
	var b strings.Builder
	for _, m := range machines {
		state := "not started"
		if m.Process != nil {
			state = "stopped"
		}
		if m.running() {
			state = "running"
		}
		fmt.Fprintf(&b, "\n--- machine %s (%s, api %s, journal %s) ---\n%s",
			m.name, state, m.url, m.sockets.dataDir, m.logs())
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
