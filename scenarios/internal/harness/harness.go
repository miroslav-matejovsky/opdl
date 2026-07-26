package harness

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
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

	"github.com/miroslav-matejovsky/opdl/builder/build"
	"github.com/miroslav-matejovsky/opdl/scenarios/internal/procrun"
	"github.com/miroslav-matejovsky/opdl/scenarios/internal/semaphore"
	"github.com/miroslav-matejovsky/opdl/scenarios/internal/waitfor"
	"github.com/miroslav-matejovsky/opdl/utils/testnet"
)

// This file is the scenario harness: how a scenario builds a project and runs
// the machines it produces. Everything here drives external processes the way a
// customer would, so a scenario never imports platform code. The one build-time
// dependency is builder/build, which the harness calls in-process to produce the
// deployment packages a customer would build with the CLI.

// scenarioSite is the site every scenario blueprint deploys to. Scenarios cover
// one site at a time, so the site is fixed here rather than threaded through
// every call. A scenario spanning two sites would take it as an argument again.
const scenarioSite = "local"

//go:embed testdata/project.hcl.tmpl
var projectTemplate string

var (
	// budget bounds the total number of platform processes the suite runs at once.
	budget = semaphore.New(max(2, runtime.GOMAXPROCS(0)/2))
	// buildBudget bounds concurrent builder compilations / executions to 2.
	buildBudget = semaphore.New(2)
)

// machineFixture is one machine of a scenario blueprint: its identity, the
// loopback address it deploys on, and its local redundancy policy.
//
// Every fixture states the standby policy explicitly. The blueprint contract
// requires it, and a scenario that let it default would be testing a decision
// nobody made.
type machineFixture struct {
	name string
	ip   string
	// standbyDisabled opts the machine out of a second local process.
	standbyDisabled bool
	// eventStorageDisabled opts the machine out of an Event Fabric entirely, by
	// authoring no platform.event_storage block. The instance then binds its API
	// and nothing else, and serves no domain operation because it has no journal
	// to serve one from.
	eventStorageDisabled bool
}

// projectFixtures are the blueprints scenarios build from, keyed by project.
//
// The fixture model is the single source of each machine's name, address, and
// standby policy; only the ports are decided per run. Keeping the machines here
// rather than in checked-in HCL is what lets the harness reserve ports on the
// right loopback address before rendering the blueprint that names them.
var projectFixtures = map[string][]machineFixture{
	// The smallest thing the platform runs: one machine deploying one Primary
	// Instance, with no standby and no event storage. There is no site to
	// coordinate with, nothing to fail over to, and no journal, so the deployment
	// is one process that binds one listener.
	"simple": {
		{name: "node-a", ip: "127.0.0.1", standbyDisabled: true, eventStorageDisabled: true},
	},
	// The local redundancy pair: one machine deploying both instances, with no
	// event storage. Two processes share one lease file and one health contract,
	// which is the whole subject of the redundancy scenarios; a journal would add
	// startup time without adding anything they assert.
	"redundancy": {
		{name: "node-a", ip: "127.0.0.1", eventStorageDisabled: true},
	},
}

// renderedMachine is one machine's template data: its fixture identity plus the
// ports reserved for this run.
type renderedMachine struct {
	Name string
	IP   string
	// The Primary Instance's ports and data directory.
	APIPort           int
	DataDir           string
	JetStreamStoreDir string
	ClientPort        int
	ClusterPort       int
	// The Standby Instance's, empty or zero when the machine deploys none. The
	// two instances run together on one host, so every one of these is its own
	// listener or directory and none may repeat.
	StandbyAPIPort           int
	StandbyDataDir           string
	StandbyJetStreamStoreDir string
	StandbyClientPort        int
	StandbyClusterPort       int
	StandbyDisabled          bool
	// LeaseFile is the machine-wide Primary Ownership lease file both instances
	// share, set only when the machine deploys a standby.
	LeaseFile string
	// EventStorageDisabled leaves the event_storage block out of both instances,
	// which is what a deployment with no journal looks like in a blueprint.
	EventStorageDisabled bool
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
// A machine lock and a Windows Service name are both kernel objects in a
// machine-wide namespace. Scenarios may build the same project and machine names
// and they run in parallel, so without this they would compete for one another's
// ownership.
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
	// standbyAPIPort is the Standby Instance's own loopback API port, zero on a
	// machine that deploys no standby. Each instance binds its own address for
	// its whole lifetime, which is what lets a scenario ask a Passive instance
	// about itself.
	standbyAPIPort int
	client         int
	cluster        int
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
//	  smoke/
//	    BuildAndRunSingleMachine/
//	      blueprints/    rendered project.hcl
//	      out/           built machine packages and manifests
//	      work/          config-*.toml, journal-*, data-*
//	      control/       marker files
func scenarioDir(t *testing.T) string {
	t.Helper()
	testName := scenarioKey(t.Name())

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

// scenarioKey turns a running test's name into the scratch root that belongs to
// it.
//
// The runner names a scenario "<category>/<Scenario>", and t.Run appends a
// segment per subtest below that. The scratch root is the scenario's, not the
// subtest's and not the category's, so this keeps the first two segments and
// drops whatever a subtest added. A one-segment name is kept whole, so a
// scenario driven directly from a Go test still gets its own root.
func scenarioKey(name string) string {
	parts := strings.Split(name, "/")
	if len(parts) > 2 {
		parts = parts[:2]
	}
	return filepath.Join(parts...)
}

// ScenarioDir returns this scenario's scratch root, the parent of its
// blueprints/, out/, work/, and control/ subdirectories. A scenario composes the
// subdirectory it wants under this root.
func ScenarioDir(t *testing.T) string {
	t.Helper()
	return scenarioDir(t)
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

// The two instance roles, as the packaged runtime spells them: the -instance
// launch argument, the directory each instance is authored under, and the
// process role on every event that instance states.
const (
	// RolePrimary is the Primary Instance.
	RolePrimary = "primary"
	// RoleStandby is the Standby Instance, deployed only when a machine's
	// blueprint enables one.
	RoleStandby = "standby"
)

// dataDirFor is one instance's own platform data root.
func dataDirFor(workDir, machine, role string) string {
	return filepath.ToSlash(filepath.Join(workDir, "data-"+machine, role))
}

// jetstreamStoreDirFor is one instance's own JetStream file store directory.
func jetstreamStoreDirFor(workDir, machine, role string) string {
	return filepath.ToSlash(filepath.Join(workDir, "journal-"+machine, role))
}

// leaseFileFor is a machine's Primary Ownership lease file, shared by its two
// instances and by nothing else.
func leaseFileFor(workDir, machine string) string {
	return filepath.ToSlash(filepath.Join(workDir, "data-"+machine, "lease"))
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
		// distinct, which the builder requires. A machine with no event storage
		// runs no server and takes none.
		var p []int
		if !fixture.eventStorageDisabled {
			wanted := 2
			if !fixture.standbyDisabled {
				wanted = 4
			}
			reserved, err := testnet.Take(fixture.ip, wanted)
			require.NoError(t, err)
			p = reserved
		}

		machine := renderedMachine{
			Name:                 fixture.name,
			IP:                   fixture.ip,
			APIPort:              takeAPIPort(),
			DataDir:              dataDirFor(workDir, fixture.name, RolePrimary),
			StandbyDisabled:      fixture.standbyDisabled,
			EventStorageDisabled: fixture.eventStorageDisabled,
		}
		if !fixture.eventStorageDisabled {
			machine.JetStreamStoreDir = jetstreamStoreDirFor(workDir, fixture.name, RolePrimary)
			machine.ClientPort = p[0]
			machine.ClusterPort = p[1]
		}
		if !fixture.standbyDisabled {
			machine.StandbyAPIPort = takeAPIPort()
			machine.StandbyDataDir = dataDirFor(workDir, fixture.name, RoleStandby)
			machine.LeaseFile = leaseFileFor(workDir, fixture.name)
			if !fixture.eventStorageDisabled {
				machine.StandbyJetStreamStoreDir = jetstreamStoreDirFor(workDir, fixture.name, RoleStandby)
				machine.StandbyClientPort = p[2]
				machine.StandbyClusterPort = p[3]
			}
		}
		data.Machines = append(data.Machines, machine)
		endpoints[fixture.name] = reservedEndpoints{
			apiPort:        machine.APIPort,
			standbyAPIPort: machine.StandbyAPIPort,
			client:         machine.ClientPort,
			cluster:        machine.ClusterPort,
		}
	}

	root = filepath.Join(scenarioDir(t), "blueprints")
	projectDir := filepath.Join(root, project)
	require.NoError(t, os.MkdirAll(projectDir, 0o755))

	tmpl, err := template.New("project").Parse(projectTemplate)
	require.NoError(t, err)
	var rendered bytes.Buffer
	require.NoError(t, tmpl.Execute(&rendered, data))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "project.hcl"), rendered.Bytes(), 0o644))

	return root, endpoints
}

// buildProject drives the builder to build every machine of a blueprint into
// outDir. It calls build.Run in-process, which runs the same flow the builder
// CLI a customer uses runs.
//
// It passes only scenario-owned inputs: the rendered blueprint directory, the
// output directory, and nothing else. Where the platform source lives and what
// product line it is are the builder's concern, so the harness leaves them to
// build.Run's defaults and never names a path into the platform module. The
// platform stays a black box the builder compiles.
func buildProject(ctx context.Context, t *testing.T, blueprintsDir, outDir, project string) {
	t.Helper()
	require.NoError(t, buildBudget.Acquire(ctx, 1))
	defer buildBudget.Release(1)

	_, err := build.Run(ctx, build.Options{
		BlueprintDir: filepath.Join(blueprintsDir, project),
		OutDir:       outDir,
	})
	require.NoError(t, err, "builder build failed")
}

// machineBinary returns the path of one built machine's binary. The builder
// names each package after the machine it is for.
func machineBinary(outDir, project, machine string) string {
	return filepath.Join(outDir, project, scenarioSite, machine, machine+".exe")
}

// WinService is one instance's Windows Service identity, as read back from a
// package manifest.
type WinService struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

// Launch is one instance's launch record in a package manifest.
type Launch struct {
	Service WinService `json:"service"`
	Args    []string   `json:"args"`
}

// PackageManifest is the subset of a built package's manifest a scenario reads.
// It is re-declared here rather than imported from the builder so a scenario
// checks the shipped manifest contract as a black box.
type PackageManifest struct {
	MachineProfile string  `json:"machine_profile"`
	Primary        Launch  `json:"primary"`
	Standby        *Launch `json:"standby"`
}

// ReadManifest reads and decodes a built machine's package manifest.
func ReadManifest(t *testing.T, binaryPath string) PackageManifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(binaryPath), "manifest.json"))
	require.NoError(t, err)
	var manifest PackageManifest
	require.NoError(t, json.Unmarshal(data, &manifest))
	require.NotEmpty(t, manifest.Primary.Args)
	return manifest
}

// Sockets are one machine's addresses and its local directories.
//
// Every value was rendered into the blueprint before the build and is carried
// only so a scenario can reach a machine, inspect its event record, and assert
// what the deployment derived. The runtime configuration contains none of
// these values.
//
// The API address and each instance's runtime directory are an instance's rather
// than a machine's, so the descriptor resolves them per instance and the
// configuration file cannot state either.
//
// The two data directories are how a scenario reads what an instance stated:
// each one holds that instance's events/events.jsonl. See operationEvents.
//
// There is no monitor address. The platform runs no NATS monitoring listener;
// each instance's event record is the local operational surface.
type Sockets struct {
	DataDir           string
	StandbyDataDir    string
	JetStreamStoreDir string

	// API, StandbyAPI, Client, and Cluster are the blueprint's, for reaching a
	// machine and for assertions. Nothing writes them to a config file.
	// StandbyAPI is the Standby Instance's own address, empty on a machine that
	// deploys no standby; each instance binds its own for its whole lifetime.
	API        string
	StandbyAPI string
	Client     string
	Cluster    string
}

// Site is the machines of one built project under a scenario's control.
//
// Every machine is prepared before any is started, because each one's
// configuration names the others: the site's NATS nodes route to each other's
// cluster addresses, and on one host those addresses are not the ones the
// deployment derived. Preparing the site as a whole is what lets a scenario
// start the machines in any order, or leave one deliberately absent.
type Site struct {
	project  string
	outDir   string
	workDir  string
	Machines []*Machine
}

// DeploySite renders the project's blueprint with allocated ports, builds every
// machine from it, and prepares each one to run.
//
// Building and preparing are one call because the two are not independent: the
// NATS ports are blueprint values, so they must be chosen before the build rather
// than handed to the runtime after it. That is the point. A scenario exercises
// the same contract a customer build does, instead of a runtime override path
// that no deployment uses.
func DeploySite(ctx context.Context, t *testing.T, outDir, workDir, project string) *Site {
	t.Helper()

	fixtures := projectFixtures[project]
	nMachines := len(fixtures)
	require.NoError(t, budget.Acquire(ctx, nMachines))
	t.Cleanup(func() {
		budget.Release(nMachines)
	})

	blueprints, endpoints := stageBlueprint(t, project, workDir)
	buildProject(ctx, t, blueprints, outDir, project)

	s := &Site{project: project, outDir: outDir, workDir: workDir}
	for _, fixture := range fixtures {
		reserved := endpoints[fixture.name]
		sockets := Sockets{
			// The Primary Instance's general data root, matching the path authored
			// in the blueprint.
			DataDir: filepath.FromSlash(dataDirFor(workDir, fixture.name, RolePrimary)),
			// The API address the builder resolved: this machine's authored
			// local_port on 127.0.0.1. A scenario reaches a machine here rather
			// than at an address it chose, because it no longer chooses one.
			API: net.JoinHostPort("127.0.0.1", strconv.Itoa(reserved.apiPort)),
		}
		// A machine with no event storage has no journal and binds no Event Fabric
		// listener, so it has no address or store directory to carry. Composing
		// one from the zero port would name a listener nothing opens.
		if !fixture.eventStorageDisabled {
			sockets.JetStreamStoreDir = filepath.FromSlash(jetstreamStoreDirFor(workDir, fixture.name, RolePrimary))
			sockets.Client = net.JoinHostPort(fixture.ip, strconv.Itoa(reserved.client))
			sockets.Cluster = net.JoinHostPort(fixture.ip, strconv.Itoa(reserved.cluster))
		}
		if !fixture.standbyDisabled {
			sockets.StandbyDataDir = filepath.FromSlash(dataDirFor(workDir, fixture.name, RoleStandby))
			sockets.StandbyAPI = net.JoinHostPort("127.0.0.1", strconv.Itoa(reserved.standbyAPIPort))
		}
		s.Machines = append(s.Machines, prepareMachine(t, s, fixture.name, sockets))
	}

	return s
}

// Machine returns one prepared machine of the site by name.
func (s *Site) Machine(t *testing.T, name string) *Machine {
	t.Helper()
	for _, m := range s.Machines {
		if m.Name == name {
			return m
		}
	}
	require.FailNowf(t, "no such machine", "%s is not a machine of project %s", name, s.project)
	return nil
}

// StartSite starts every platform instance the site deploys, all at once, and
// only then waits for each machine's Primary Instance to serve.
//
// Every instance, not only the primaries. The journal's replica count is the
// site's storage instance count, and JetStream cannot place three replicas until
// three servers are up, so a site whose third storage instance is a machine's
// Standby Instance would sit in "no suitable peers for placement" if it were
// started primaries-only. That makes starting the standbys part of bringing a
// site up rather than an extra a redundancy scenario opts into.
//
// except names machines to leave down, for a scenario whose subject is a machine
// that is absent. Only a machine outside the storage selection can be held back;
// holding back a storage instance costs the journal a replica it cannot place.
func (s *Site) StartSite(ctx context.Context, t *testing.T, except ...string) {
	t.Helper()
	for _, m := range s.Machines {
		if slices.Contains(except, m.Name) {
			continue
		}
		m.Start(ctx, t)
		if manifest := ReadManifest(t, m.BinaryPath); manifest.Standby != nil {
			m.Standby = m.StartManaged(ctx, t, RoleStandby, manifest.Standby.Args)
		}
	}
	for _, m := range s.Machines {
		if slices.Contains(except, m.Name) {
			continue
		}
		WaitForActiveInstance(ctx, t, m)
	}
}

// Machine is one platform process under a scenario's control: prepared, and
// running once started.
type Machine struct {
	*procrun.Process
	// Name is the deployment machine identity.
	Name string
	// URL is the base URL of the Primary Instance's API, known from the moment
	// the machine is prepared, whether or not it is running.
	URL string
	// StandbyURL is the base URL of the Standby Instance's own API, empty on a
	// machine that deploys no standby.
	StandbyURL string
	// Sockets are the addresses and storage it was configured with. A restart
	// reuses them, which is what makes replaying its own journal possible. They
	// are also where a scenario reads either instance's event record, which is
	// the only local surface the runtime writes.
	Sockets Sockets

	BinaryPath string
	configPath string
	LaunchArgs []string
	// Standby is this machine's Standby Instance once StartSite has started one.
	Standby *ManagedProcess
	stopped bool
}

// ManagedProcess is one explicitly named primary or standby process, for a
// scenario that needs to stop one process without stopping the other process of
// the same machine.
type ManagedProcess struct {
	*procrun.Process
	Role string
}

// IsRunning reports whether the machine's process is still alive.
func (m *Machine) IsRunning() bool {
	if m.Process == nil || m.stopped {
		return false
	}
	return m.Running()
}

// Exited reports that this machine had a process of its own and that process is
// gone. It is the fail-fast signal for a wait: there is no point polling a
// machine that has died.
//
// It is deliberately not the negation of IsRunning. A machine whose processes are
// launched with StartManaged never has a process of its own, so IsRunning is
// false for it from the start. Aborting on that would fail every wait against
// such a machine before the first poll, while the instances serving its endpoint
// are perfectly healthy.
func (m *Machine) Exited() bool {
	return m.Process != nil && !m.IsRunning()
}

// StartManaged starts one explicitly named instance of the machine and returns a
// handle a scenario can stop on its own.
func (m *Machine) StartManaged(ctx context.Context, t *testing.T, role string, args []string) *ManagedProcess {
	t.Helper()
	commandArgs := append([]string{"-config", m.configPath}, args...)
	cmd := exec.CommandContext(ctx, m.BinaryPath, commandArgs...)
	proc, err := procrun.Start(cmd)
	require.NoError(t, err)
	p := &ManagedProcess{
		Process: proc,
		Role:    role,
	}
	t.Cleanup(func() {
		_ = p.Kill()
	})
	return p
}

// DiagStringer adapts a func into a fmt.Stringer, so a scenario can defer
// rendering a diagnostic until a wait actually fails.
type DiagStringer func() string

func (d DiagStringer) String() string { return d() }

// WaitFor polls cond until it holds, aborts early when abort says to, and fails
// the scenario with diag rendered at failure time.
func WaitFor(t *testing.T, what string, timeout, interval time.Duration, cond func() bool, abort waitfor.Abort, diag fmt.Stringer) {
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

// operationEvents reads each deployed instance's canonical JSONL event record.
// Restarts append to the same per-instance file.
func operationEvents(m *Machine) string {
	paths := []string{filepath.Join(m.Sockets.DataDir, "events", "events.jsonl")}
	if m.Sockets.StandbyDataDir != "" {
		paths = append(paths, filepath.Join(m.Sockets.StandbyDataDir, "events", "events.jsonl"))
	}
	var b strings.Builder
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			fmt.Fprintf(&b, "--- %s: %v ---\n", path, err)
			continue
		}
		fmt.Fprintf(&b, "--- %s ---\n%s", path, data)
	}
	if b.Len() == 0 {
		return "(none)\n"
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
func prepareMachine(t *testing.T, s *Site, name string, reserved Sockets) *Machine {
	t.Helper()
	binaryPath := machineBinary(s.outDir, s.project, name)
	require.FileExists(t, binaryPath)

	// The file carries the journal's bounds only when there is a journal to
	// bound. A machine with no event storage has no Event Fabric server to start
	// and no projection to lag, so writing them would be writing settings nothing
	// reads. The store directory is the harness's own record of that decision.
	configPath := filepath.Join(s.workDir, "config-"+name+".toml")
	require.NoError(t, os.WriteFile(configPath, platformConfig(reserved.JetStreamStoreDir != ""), 0o644))

	m := &Machine{
		Name:       name,
		URL:        "http://" + reserved.API,
		Sockets:    reserved,
		BinaryPath: binaryPath,
		configPath: configPath,
		LaunchArgs: ReadManifest(t, binaryPath).Primary.Args,
	}
	if reserved.StandbyAPI != "" {
		m.StandbyURL = "http://" + reserved.StandbyAPI
	}
	t.Cleanup(m.Stop)
	return m
}

// platformConfig renders runtime-only platform settings. Storage paths,
// addresses, and backend selection are compiled from the blueprint into the
// descriptor. A machine's two instances read this one file, so it cannot hold
// per-instance values.
//
// The three tolerances are deliberately looser than platform/config.toml's 30s.
// They bound how long a machine puts up with a slow environment before giving
// up, and a scenario suite is a slow environment on purpose: it builds the
// platform and starts it on a busy host. No scenario asserts on these
// values, so raising them removes a false failure without weakening anything: a
// machine that genuinely never catches up still fails, on the assertion that was
// actually being made.
//
// A machine with no event storage gets none of the three. They all bound a site
// journal, which that deployment does not have, and leaving them out is what
// proves the runtime does not require them: the scenario's platform binary reads
// this exact file.
func platformConfig(eventStorage bool) []byte {
	config := `read_header_timeout = "5s"
shutdown_timeout = "10s"
`
	if !eventStorage {
		return []byte(config)
	}
	return []byte(config + `lag_bound = "2m"
`)
}

// Start runs a prepared machine.
func (m *Machine) Start(ctx context.Context, t *testing.T) {
	t.Helper()
	require.Nil(t, m.Process, "%s is already started", m.Name)
	args := append([]string{"-config", m.configPath}, m.LaunchArgs...)
	cmd := exec.CommandContext(ctx, m.BinaryPath, args...)
	proc, err := procrun.Start(cmd)
	require.NoError(t, err)
	m.Process = proc
	m.stopped = false
}

// Stop force-stops the machine. A graceful child interrupt is not portable, and
// stopping hard is also the more demanding test: the journal is on disk, so a
// node that is killed must still come back to the same state.
func (m *Machine) Stop() {
	if m.stopped || m.Process == nil {
		return
	}
	m.stopped = true
	_ = m.Kill()
}

// Output returns the machine's captured output, and says so when the machine was
// never started rather than dereferencing a process that does not exist.
//
// It does not stop the machine. The output buffer is mutex guarded, so reading
// it while the process is still writing is safe and is what diagnostics need:
// stopping a machine in order to find out what it said would destroy the state
// the failure is about.
func (m *Machine) Output() string {
	if m.Process == nil {
		return "(never started)\n"
	}
	return m.Logs()
}

// Diagnose renders everything worth knowing when a scenario fails: what each
// machine printed, whether it was running, and where it answers.
func Diagnose(machines []*Machine) string {
	var b strings.Builder
	for _, m := range machines {
		state := "not started"
		if m.Process != nil {
			state = "stopped"
		}
		if m.IsRunning() {
			state = "running"
		}
		fmt.Fprintf(&b, "\n--- machine %s (%s, api %s, journal %s) ---\n%s",
			m.Name, state, m.URL, m.Sockets.JetStreamStoreDir, m.Output())
		fmt.Fprintf(&b, "\n--- machine %s operational events ---\n%s", m.Name, operationEvents(m))
	}
	return b.String()
}

// lazily defers rendering part of a failure message until an assertion actually
// fails.
//
// A message argument is evaluated where it is written, not where it is
// formatted, so passing Diagnose(...) straight into a wait captured every
// machine's output before the wait had had a chance to fail. The logs that
// explained the failure were then precisely the ones missing from it, because
// they had not been written yet. testify formats a message only on failure, so a
// Stringer is rendered at the moment worth describing.
type lazily func() string

func (l lazily) String() string { return l() }

// Diagnostics renders Diagnose for these machines, at failure time.
func Diagnostics(machines ...*Machine) fmt.Stringer {
	return lazily(func() string { return Diagnose(machines) })
}
