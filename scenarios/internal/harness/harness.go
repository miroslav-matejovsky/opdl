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

// loopbackHost is the address every platform listener a scenario deploys is
// resolved onto, and the one every port reservation is taken against. A machine
// may be authored on a different loopback address — its services bind the
// machine's own ip — but nothing the platform binds ever leaves this one.
const loopbackHost = "127.0.0.1"

// The machine names the fixtures below deploy. Every fixture starts from node-a
// and adds machines in order, so a scenario that wants "the first machine" and
// one that wants "the machine with a standby" name the same thing.
const (
	machineA = "node-a"
	machineB = "node-b"
)

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
	// probe is the service health-check policy this machine is authored with.
	// The zero value renders slowProbe.
	probe probeFixture
}

// probeFixture is one machine's authored service health-check timings.
//
// It is per fixture rather than fixed in the template because the two kinds of
// scenario want opposite things from it. A scenario about something else wants
// the probes quiet and out of the way; a scenario about health wants
// convergence to happen inside a poll rather than a coffee break.
type probeFixture struct {
	interval string
	timeout  string
	retries  int
}

// slowProbe is what a machine is authored with unless it says otherwise. It is
// slow on purpose: nothing listens on the probe port in a scenario that is not
// about health, so a fast interval would fill the log with connection refusals
// that have nothing to do with what failed.
var slowProbe = probeFixture{interval: "10s", timeout: "2s", retries: 3}

// fastProbe is for the health scenarios. The retry threshold is reached in
// about a second and a report stays current for `2*interval + timeout`, which
// is 1.2s — short enough that an observer that stops reporting goes visibly
// stale inside a poll rather than outliving the scenario.
var fastProbe = probeFixture{interval: "500ms", timeout: "200ms", retries: 2}

// orDefault fills in slowProbe for a fixture that stated no policy.
func (p probeFixture) orDefault() probeFixture {
	if p.interval == "" {
		return slowProbe
	}
	return p
}

// projectFixtures are the blueprints scenarios build from, keyed by project.
//
// The fixture model is the single source of each machine's name, address, and
// standby policy; only the ports are decided per run. Keeping the machines here
// rather than in checked-in HCL is what lets the harness reserve ports on the
// right loopback address before rendering the blueprint that names them.
var projectFixtures = map[string][]machineFixture{
	// The smallest thing the platform runs: one machine deploying one Primary
	// Instance, with no standby.
	"simple": {
		{name: machineA, ip: loopbackHost, standbyDisabled: true},
	},
	// The local redundancy pair: one machine deploying both instances.
	"redundancy": {
		{name: machineA, ip: loopbackHost},
	},
	// The same pair, for the scenario that starts both of its instances at once
	// rather than in a fixed order.
	//
	// It is a fixture of its own only because of the probe policy. That scenario
	// asks whether both instances kept watching the machine's service across a
	// contested start, so the service is bound and the probes have to be fast
	// enough to answer inside the scenario; the redundancy fixture is authored
	// the other way round, watching a service nothing binds.
	"startup": {
		{name: machineA, ip: loopbackHost, probe: fastProbe},
	},
	// The site the health scenarios observe: two machines on different addresses,
	// one with a Standby Instance and one without.
	//
	// The asymmetry is the point. node-a's service is reported on by two
	// observers, so disagreement and a lost observer are both visible on it;
	// node-b's is reported on by one, so an outage there leaves the site with no
	// opinion about it rather than a reduced one. A site of identical machines
	// would exercise neither.
	//
	// The two machines are on different loopback addresses because their services
	// bind the machine's own ip, which is what tells one machine's service from
	// another's on a host running both.
	"health": {
		{name: machineA, ip: loopbackHost, probe: fastProbe},
		{name: machineB, ip: "127.0.0.2", standbyDisabled: true, probe: fastProbe},
	},
}

// renderedMachine is one machine's template data: its fixture identity plus the
// ports reserved for this run.
type renderedMachine struct {
	Name string
	IP   string
	// MachineEventsFile is the machine's own event store, shared by both of its
	// instances. Every machine authors one, standby or not.
	MachineEventsFile string
	// HealthPort is where this machine's one authored service answers its health
	// check, on the machine's own ip. It is reserved from the same pool as the
	// platform's listeners rather than fixed, because a scenario may actually bind
	// it, and two scenario runs sharing one host must not both try.
	HealthPort     int
	HealthInterval string
	HealthTimeout  string
	HealthRetries  int
	// The Primary Instance's ports and local files. NATSPort is its own embedded
	// event fabric broker's cluster port, the other listener every deployed
	// instance binds.
	APIPort    int
	NATSPort   int
	EventsFile string
	StateFile  string
	LogFile    string
	// The Standby Instance's, empty or zero when the machine deploys none. The
	// two instances run together on one host, so every one of these is its own
	// listener or file and none may repeat.
	StandbyAPIPort    int
	StandbyNATSPort   int
	StandbyEventsFile string
	StandbyStateFile  string
	StandbyLogFile    string
	StandbyDisabled   bool
	// LeaseFile is the machine-wide Primary Ownership lease file both instances
	// share, set only when the machine deploys a standby.
	LeaseFile string
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
	// apiPort is the Primary Instance's loopback API port.
	apiPort int
	// standbyAPIPort is the Standby Instance's own loopback API port, zero on a
	// machine that deploys no standby.
	standbyAPIPort int
	// healthAddress is where this machine's authored service is expected to
	// answer: the machine's own ip and its reserved health port.
	healthAddress string
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
//	      work/          journal-*, data-*
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

// listenerPortsWanted counts the loopback ports a project's instances need.
//
// Every deployed instance binds two: its API listener and its own embedded
// event fabric broker. Both are reserved from the same pool, because both are
// on 127.0.0.1 and a collision between them is the same failure to bind as a
// collision between two APIs.
// One more comes from the same pool per machine: the port its authored service
// answers its health check on. It is not the platform's listener, but a scenario
// may bind it, so it is reserved rather than assumed free.
func listenerPortsWanted(fixtures []machineFixture) int {
	const perInstance = 2
	wanted := 0
	for _, fixture := range fixtures {
		wanted += perInstance + 1
		if !fixture.standbyDisabled {
			wanted += perInstance
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

// instanceDirFor is where one instance's own local files are rendered. It is a
// harness convention only: the blueprint names every file outright, and this is
// just how the harness keeps each instance of each machine apart on a host that
// several scenarios share.
func instanceDirFor(workDir, machine, role string) string {
	return filepath.ToSlash(filepath.Join(workDir, "data-"+machine, role))
}

// eventsFileFor is one instance's append-only event record.
func eventsFileFor(workDir, machine, role string) string {
	return instanceDirFor(workDir, machine, role) + "/events.jsonl"
}

// stateFileFor is one instance's durable state record, which carries its epoch.
func stateFileFor(workDir, machine, role string) string {
	return instanceDirFor(workDir, machine, role) + "/state.json"
}

// logFileFor is one instance's structured application log: what the process said
// it was doing, beside the events file, which is what it stated.
func logFileFor(workDir, machine, role string) string {
	return instanceDirFor(workDir, machine, role) + "/platform.log"
}

// machineEventsFileFor is a machine's own event store: the file both of its
// instances append machine-scoped events to. Every machine has one, standby or
// not, so it sits beside the lease rather than with an instance's files.
func machineEventsFileFor(workDir, machine string) string {
	return filepath.ToSlash(filepath.Join(workDir, "data-"+machine, "machine-events.jsonl"))
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

	// Every listener comes from one reservation on 127.0.0.1, because that is the
	// only interface any of them is resolved onto. Reserving per machine ip would
	// say nothing about whether two machines had been given the same loopback
	// port, and the collision would only appear as the second machine failing to
	// bind.
	ports, err := testnet.Take(loopbackHost, listenerPortsWanted(fixtures))
	require.NoError(t, err)
	nextPort := 0
	takePort := func() int {
		port := ports[nextPort]
		nextPort++
		return port
	}

	for _, fixture := range fixtures {
		probe := fixture.probe.orDefault()
		machine := renderedMachine{
			Name:              fixture.name,
			IP:                fixture.ip,
			MachineEventsFile: machineEventsFileFor(workDir, fixture.name),
			HealthPort:        takePort(),
			HealthInterval:    probe.interval,
			HealthTimeout:     probe.timeout,
			HealthRetries:     probe.retries,
			APIPort:           takePort(),
			NATSPort:          takePort(),
			EventsFile:        eventsFileFor(workDir, fixture.name, RolePrimary),
			StateFile:         stateFileFor(workDir, fixture.name, RolePrimary),
			LogFile:           logFileFor(workDir, fixture.name, RolePrimary),
			StandbyDisabled:   fixture.standbyDisabled,
		}
		if !fixture.standbyDisabled {
			machine.StandbyAPIPort = takePort()
			machine.StandbyNATSPort = takePort()
			machine.StandbyEventsFile = eventsFileFor(workDir, fixture.name, RoleStandby)
			machine.StandbyStateFile = stateFileFor(workDir, fixture.name, RoleStandby)
			machine.StandbyLogFile = logFileFor(workDir, fixture.name, RoleStandby)
			machine.LeaseFile = leaseFileFor(workDir, fixture.name)
		}
		data.Machines = append(data.Machines, machine)
		endpoints[fixture.name] = reservedEndpoints{
			apiPort:        machine.APIPort,
			standbyAPIPort: machine.StandbyAPIPort,
			healthAddress:  net.JoinHostPort(fixture.ip, strconv.Itoa(machine.HealthPort)),
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

// Sockets are one machine's addresses and its local files.
//
// Every value was rendered into the blueprint before the build and is carried
// only so a scenario can reach a machine, inspect its event record, and assert
// what the deployment derived. Nothing here is written beside the binary: the
// blueprint is the only place these are stated, and the build compiles them in.
//
// The API address and each instance's local files are an instance's rather than
// a machine's, so the descriptor resolves them per instance.
//
// The events files are how a scenario reads what an instance stated; the state
// files are how it reads which incarnation an instance is on. See
// operationEvents and InstanceEpoch.
//
// There is no monitor address. Each instance's event record is the local
// operational surface.
type Sockets struct {
	// MachineEventsFile is the machine's own event store, which both instances
	// append machine-scoped events to. It is the machine's rather than an
	// instance's, so a scenario reads the machine's account of a failover from
	// one file instead of stitching two instances' records together.
	MachineEventsFile string

	EventsFile string
	StateFile  string
	// LogFile is the instance's structured application log, which a failing
	// scenario dumps beside the events its machine stated.
	LogFile string
	// The Standby Instance's own, empty on a machine that deploys no standby.
	StandbyEventsFile string
	StandbyStateFile  string
	StandbyLogFile    string

	// API and StandbyAPI are the blueprint's, for reaching a machine and for
	// assertions.
	// StandbyAPI is the Standby Instance's own address, empty on a machine that
	// deploys no standby; each instance binds its own for its whole lifetime.
	API        string
	StandbyAPI string

	// HealthAddress is where this machine's authored service is expected to
	// answer its health check: the machine's own ip and its reserved port. It is
	// not the platform's listener — nothing binds it unless a scenario does, and
	// a scenario that does not is one whose machines are watching a service that
	// is not there, which is the honest default.
	HealthAddress string
}

// Site is the machines of one built project under a scenario's control.
type Site struct {
	project string
	outDir  string
	workDir string

	Machines []*Machine
}

// DeploySite builds a project and returns its site ready to run.
func DeploySite(ctx context.Context, t *testing.T, outDir, workDir, project string) *Site {
	t.Helper()

	fixtures, ok := projectFixtures[project]
	require.Truef(t, ok, "no blueprint fixture for project %q", project)
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
			// The machine's own store, matching the path authored in the blueprint.
			MachineEventsFile: filepath.FromSlash(machineEventsFileFor(workDir, fixture.name)),
			// The Primary Instance's own local files, matching the paths authored
			// in the blueprint.
			EventsFile: filepath.FromSlash(eventsFileFor(workDir, fixture.name, RolePrimary)),
			StateFile:  filepath.FromSlash(stateFileFor(workDir, fixture.name, RolePrimary)),
			LogFile:    filepath.FromSlash(logFileFor(workDir, fixture.name, RolePrimary)),
			// The API address the builder resolved: this machine's authored
			// local_port on 127.0.0.1. A scenario reaches a machine here rather
			// than at an address it chose, because it no longer chooses one.
			API:           net.JoinHostPort(loopbackHost, strconv.Itoa(reserved.apiPort)),
			HealthAddress: reserved.healthAddress,
		}
		if !fixture.standbyDisabled {
			sockets.StandbyEventsFile = filepath.FromSlash(eventsFileFor(workDir, fixture.name, RoleStandby))
			sockets.StandbyStateFile = filepath.FromSlash(stateFileFor(workDir, fixture.name, RoleStandby))
			sockets.StandbyLogFile = filepath.FromSlash(logFileFor(workDir, fixture.name, RoleStandby))
			sockets.StandbyAPI = net.JoinHostPort(loopbackHost, strconv.Itoa(reserved.standbyAPIPort))
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
// site's storage instance count, so a site whose third storage instance is a
// machine's Standby Instance cannot place its replicas if it is started
// primaries-only. That makes starting the standbys part of bringing a site up
// rather than an extra a redundancy scenario opts into.
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
	cmd := exec.CommandContext(ctx, m.BinaryPath, args...)
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

// StartTogether starts both of a machine's instances as close to simultaneously
// as the host allows, and returns a handle on each.
//
// It exists because "both instances start at once" is a real deployment
// condition — a machine that reboots starts its two Windows Services together —
// and starting them one after the other does not reproduce it. Each command is
// built on the calling goroutine, so the only work left inside the race is the
// launch itself, and both launches are released from one barrier.
//
// Which instance wins the machine's lease is deliberately not decided here. A
// scenario that calls this must hold for either.
func (m *Machine) StartTogether(ctx context.Context, t *testing.T, manifest PackageManifest) (primary, standby *ManagedProcess) {
	t.Helper()
	require.NotNilf(t, manifest.Standby, "%s deploys no standby, so it has no second instance to start with", m.Name)

	type launch struct {
		role string
		args []string
		proc *procrun.Process
		err  error
	}
	launches := []launch{
		{role: RolePrimary, args: manifest.Primary.Args},
		{role: RoleStandby, args: manifest.Standby.Args},
	}

	release := make(chan struct{})
	var launched sync.WaitGroup
	for i := range launches {
		cmd := exec.CommandContext(ctx, m.BinaryPath, launches[i].args...)
		launched.Go(func() {
			<-release
			launches[i].proc, launches[i].err = procrun.Start(cmd)
		})
	}
	close(release)
	launched.Wait()

	// Both outcomes are asserted on the calling goroutine. require.* inside the
	// launch goroutines would call runtime.Goexit there and leave the other
	// instance running with nothing to stop it.
	started := make([]*ManagedProcess, 0, len(launches))
	for i := range launches {
		require.NoErrorf(t, launches[i].err, "%s: starting its %s instance", m.Name, launches[i].role)
		p := &ManagedProcess{Process: launches[i].proc, Role: launches[i].role}
		t.Cleanup(func() { _ = p.Kill() })
		started = append(started, p)
	}
	return started[0], started[1]
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
	// The machine's own store comes first: on a failure that involves ownership
	// it is the one file that holds both instances' side of it.
	paths := []string{m.Sockets.MachineEventsFile, m.Sockets.EventsFile}
	if m.Sockets.StandbyEventsFile != "" {
		paths = append(paths, m.Sockets.StandbyEventsFile)
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

// StoredEvent is the part of a stored envelope a scenario asserts on: what
// happened, which level it belongs to, and which instance stated it.
//
// It is declared here rather than imported from the platform so a scenario reads
// the shipped file as a black box, the same way an operator's tooling would.
type StoredEvent struct {
	Type   string `json:"type"`
	Scope  string `json:"scope"`
	Origin struct {
		ProcessRole string `json:"process_role"`
	} `json:"origin"`
}

// MachineEvents reads a machine's own event store: the file both of its
// instances append machine-scoped events to, in the order they were appended.
//
// The store is append-only and nothing in the platform reads it back, so this
// is the reader it is written for — an assertion, or an operator, opening a
// JSON Lines file.
func MachineEvents(t *testing.T, m *Machine) []StoredEvent {
	t.Helper()
	data, err := os.ReadFile(m.Sockets.MachineEventsFile)
	require.NoError(t, err, "the machine's own event store is opened at startup, so it exists")

	var stored []StoredEvent
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event StoredEvent
		require.NoErrorf(t, json.Unmarshal([]byte(line), &event), "machine store line does not decode: %s", line)
		stored = append(stored, event)
	}
	return stored
}

// LogRecord is the part of an application log line a scenario asserts on: what
// the process said, at which level, and which instance said it.
//
// It is declared here rather than imported from the platform because the log
// file is read as a black box, the same way an operator's tooling would read it.
type LogRecord struct {
	Level    string `json:"level"`
	Message  string `json:"msg"`
	Machine  string `json:"machine"`
	Instance string `json:"instance"`
}

// ApplicationLog reads one instance's structured application log.
//
// It is a different file from the instance's event record and answers a
// different question: the event record is what the instance stated, and this is
// what the process was doing. Nothing in the platform reads it back, so this is
// the reader it is written for.
func ApplicationLog(t *testing.T, path string) []LogRecord {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoErrorf(t, err, "the instance opens its application log at startup, so %s exists", path)

	var records []LogRecord
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var record LogRecord
		require.NoErrorf(t, json.Unmarshal([]byte(line), &record), "application log line does not decode: %s", line)
		records = append(records, record)
	}
	return records
}

// InstanceEpochs is what an instance's state file says about its incarnations:
// the total, and how that total was reached.
type InstanceEpochs struct {
	// Epoch is how many incarnations the instance has had, of both kinds.
	Epoch uint64
	// Process is how many times it has been launched.
	Process uint64
	// Activation is how many times it has taken Primary Ownership.
	Activation uint64
}

// InstanceEpoch reads one instance's epochs out of its state file.
//
// The epoch is how a scenario tells one incarnation of an instance from the
// next: it advances by exactly one when a process starts and again when the
// instance takes Primary Ownership. The two counts are read as well as the total
// because a scenario that killed a process and a scenario that moved ownership
// both raise the total, and only the counts tell them apart. It is read out of
// the file rather than off an endpoint because the file is the durable half, and
// outliving the process is the whole point of it.
func InstanceEpoch(t *testing.T, stateFile string) InstanceEpochs {
	t.Helper()
	data, err := os.ReadFile(stateFile)
	require.NoErrorf(t, err, "reading the instance state file %s", stateFile)
	var state struct {
		Epoch        uint64 `json:"epoch"`
		ProcessEpoch struct {
			Count uint64 `json:"count"`
		} `json:"process_epoch"`
		ActivationEpoch struct {
			Count uint64 `json:"count"`
		} `json:"activation_epoch"`
	}
	require.NoErrorf(t, json.Unmarshal(data, &state), "decoding the instance state file %s", stateFile)
	return InstanceEpochs{
		Epoch:      state.Epoch,
		Process:    state.ProcessEpoch.Count,
		Activation: state.ActivationEpoch.Count,
	}
}

// prepareMachine takes a handle on one built machine without starting it.
//
// Preparing and starting are separate so a scenario can know where a machine
// will answer before it is running. That is what lets a machine be deliberately
// offline for part of a scenario while something else is already configured to
// call it, which is the only way to observe what the platform does about an
// expected machine that is not there.
//
// There is nothing to write. Every setting a machine runs with is authored in
// the rendered blueprint and compiled into its binary, so preparing a machine is
// finding the binary the build produced and reading the launch arguments out of
// its manifest.
func prepareMachine(t *testing.T, s *Site, name string, reserved Sockets) *Machine {
	t.Helper()
	binaryPath := machineBinary(s.outDir, s.project, name)
	require.FileExists(t, binaryPath)

	m := &Machine{
		Name:       name,
		URL:        "http://" + reserved.API,
		Sockets:    reserved,
		BinaryPath: binaryPath,
		LaunchArgs: ReadManifest(t, binaryPath).Primary.Args,
	}
	if reserved.StandbyAPI != "" {
		m.StandbyURL = "http://" + reserved.StandbyAPI
	}
	t.Cleanup(m.Stop)
	return m
}

// Start runs a prepared machine.
func (m *Machine) Start(ctx context.Context, t *testing.T) {
	t.Helper()
	require.Nil(t, m.Process, "%s is already started", m.Name)
	cmd := exec.CommandContext(ctx, m.BinaryPath, m.LaunchArgs...)
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
		fmt.Fprintf(&b, "\n--- machine %s (%s, api %s) ---\n%s",
			m.Name, state, m.URL, m.Output())
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
