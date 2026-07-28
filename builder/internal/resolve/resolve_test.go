package resolve_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/builder/deployment"
	"github.com/miroslav-matejovsky/opdl/builder/internal/blueprint"
	"github.com/miroslav-matejovsky/opdl/builder/internal/resolve"
)

// Every machine in these fixtures authors the same ports. That is the point of
// the contract: a port is one instance's policy, and what distinguishes machines
// is their ips, not giving each one a different port. What must differ is the two
// instances of one machine, which is why the standby's ports are offset.
const (
	apiPort        = 8080
	standbyAPIPort = 8081
	// Each instance's embedded event fabric broker binds its own cluster port,
	// the other listener a deployed instance takes on the machine.
	natsPort        = 6222
	standbyNATSPort = 6223
	// healthCheckPort is the service endpoint the machine's one service is probed
	// on. It is a listener on the same machine as the two API ports, so it is
	// distinct from both.
	healthCheckPort = 9101
)

// service builds a machine's hosted service with a role and a valid health
// check. What resolution does with it is carry the name, so the rest is here to
// keep the fixture authored the way a blueprint is rather than to be asserted
// on.
func service(name string) blueprint.Service {
	return blueprint.Service{
		Name: name,
		Role: "master",
		HealthCheck: blueprint.HealthCheck{
			Type:     "http",
			Port:     healthCheckPort,
			Path:     "/health",
			Interval: "10s",
			Timeout:  "2s",
			Retries:  3,
		},
	}
}

// machine builds a valid machine with the mandatory platform policy filled in.
func machine(name, ip string, standbyDisabled bool) blueprint.Machine {
	standby := &blueprint.Standby{Disabled: standbyDisabled}
	if !standbyDisabled {
		standby.Lease = &blueprint.Lease{
			File:                  "D:/opdl/" + name + "/lease",
			Duration:              "15s",
			RenewalInterval:       "5s",
			HealthCheckInterval:   "2s",
			FailbackStabilization: "30s",
			LagBound:              "30s",
		}
		standby.EventlogFile = eventsFile(name, "standby")
		standby.StateFile = stateFile(name, "standby")
		standby.LogFile = logFile(name, "standby")
		standby.API = &blueprint.API{LocalPort: standbyAPIPort, ReadHeaderTimeout: "5s", ShutdownTimeout: "10s"}
		standby.NATS = &blueprint.NATS{ClusterPort: standbyNATSPort}
		standby.WinService = &blueprint.WinService{Name: name + "-standby"}
	}
	return blueprint.Machine{
		Name: name, MachineProfile: "node", IP: ip, Services: []blueprint.Service{service("core-services")},
		EventstoreFile: machineEventsFile(name),
		Primary: &blueprint.Primary{
			EventlogFile: eventsFile(name, "primary"),
			StateFile:    stateFile(name, "primary"),
			LogFile:      logFile(name, "primary"),
			API:          &blueprint.API{LocalPort: apiPort, ReadHeaderTimeout: "5s", ShutdownTimeout: "10s"},
			NATS:         &blueprint.NATS{ClusterPort: natsPort},
			WinService:   &blueprint.WinService{Name: name + "-primary"},
		},
		Standby: standby,
	}
}

// machineEventsFile is the machine's own event store, shared by its instances.
func machineEventsFile(machine string) string {
	return fmt.Sprintf("D:/opdl-data/%s/machine-events.jsonl", machine)
}

// eventsFile is one instance's append-only event record.
func eventsFile(machine, role string) string {
	return fmt.Sprintf("D:/opdl-data/%s/%s/events.jsonl", machine, role)
}

// stateFile is one instance's durable state record.
func stateFile(machine, role string) string {
	return fmt.Sprintf("D:/opdl-data/%s/%s/state.json", machine, role)
}

// logFile is one instance's structured application log.
func logFile(machine, role string) string {
	return fmt.Sprintf("D:/opdl-data/%s/%s/platform.log", machine, role)
}

// companion is a second machine carrying a Standby Instance, for fixtures whose
// subject is one machine.
//
// The platform's smallest site is two machines and three platform instances, so
// a site of one machine is rejected before any rule a test is about gets a
// chance to run. Appending this keeps such a fixture legal without changing what
// it is testing: it is declared after the machine under test, so that machine
// stays plan.Machines[0].
func companion() blueprint.Machine {
	return machine("companion", "10.0.99.1", false)
}

// The one site these fixtures deploy to, and the event fabric cluster its
// machines' servers join. Every fixture is a single site, so both are fixed here
// rather than threaded through each call; what the tests are about is what
// resolution derives from them.
const (
	siteName        = "north"
	siteClusterName = "north-fabric"
)

// site builds a site with the event fabric cluster policy every site is required
// to author.
func site(machines ...blueprint.Machine) blueprint.Site {
	return blueprint.Site{
		Name:     siteName,
		NATS:     &blueprint.SiteNATS{ClusterName: siteClusterName},
		Machines: machines,
	}
}

func projectOf(sites ...blueprint.Site) *blueprint.Project {
	return &blueprint.Project{
		Name:        "customer-a",
		Environment: "production",
		Sites:       sites,
	}
}

// project is a one-machine, one-site project.
func project() *blueprint.Project {
	return projectOf(site(sensor(), companion()))
}

// sensor is a one-instance machine: it deploys no Standby Instance.
func sensor() blueprint.Machine {
	m := machine("sensor", "10.0.1.10", true)
	m.MachineProfile = "sensor-node"
	m.Services = []blueprint.Service{service("sensor-services")}
	return m
}

// machineByName returns the descriptor of one machine in a plan.
func machineByName(t *testing.T, plan *resolve.Plan, name string) deployment.Descriptor {
	t.Helper()
	for _, m := range plan.Machines {
		if m.Machine == name {
			return m
		}
	}
	t.Fatalf("machine %q not in plan", name)
	return deployment.Descriptor{}
}

func TestBuildProducesMachineDescriptors(t *testing.T) {
	plan, err := resolve.Build(project(), "acme-opdl")
	require.NoError(t, err)
	require.Equal(t, "customer-a", plan.Project)
	// The sensor under test, plus the companion that makes the site a legal size.
	require.Len(t, plan.Machines, 2)

	m := plan.Machines[0]
	require.Equal(t, "acme-opdl", m.Platform)
	require.Equal(t, "production", m.Environment)
	require.Equal(t, "north", m.Site)
	require.Equal(t, "sensor", m.Machine)
	require.Equal(t, "sensor-node", m.MachineProfile)
	require.Equal(t, "10.0.1.10", m.IP)
	require.Equal(t, []string{"sensor-services"}, m.Services)
	require.NotEmpty(t, m.Primary.APIAddress, "a machine always deploys a primary process")
	// The authored paths are carried whole. Nothing here composes them, so a
	// resolver that derived a path would show up as a mismatch rather than as a
	// runtime that quietly wrote somewhere else.
	require.Equal(t, eventsFile("sensor", "primary"), m.Primary.EventsFile)
	require.Equal(t, stateFile("sensor", "primary"), m.Primary.StateFile)
	require.Equal(t, logFile("sensor", "primary"), m.Primary.LogFile)
}

// TestBuildCarriesInstanceFiles checks each deployed instance's own files reach
// its own record, and that an undeployed instance carries none. Both instances
// are checked because a resolver that read the primary's paths for both would
// produce a descriptor that looks complete and puts two runtimes on one file.
func TestBuildCarriesInstanceFiles(t *testing.T) {
	p := projectOf(site(machine("node-a", "10.0.1.10", false), companion()))
	plan, err := resolve.Build(p, "acme-opdl")
	require.NoError(t, err)

	m := plan.Machines[0]
	require.Equal(t, eventsFile("node-a", "primary"), m.Primary.EventsFile)
	require.Equal(t, stateFile("node-a", "primary"), m.Primary.StateFile)
	require.Equal(t, logFile("node-a", "primary"), m.Primary.LogFile)
	require.NotNil(t, m.Standby)
	require.Equal(t, eventsFile("node-a", "standby"), m.Standby.EventsFile)
	require.Equal(t, stateFile("node-a", "standby"), m.Standby.StateFile)
	require.Equal(t, logFile("node-a", "standby"), m.Standby.LogFile)
}

// TestBuildCopiesStandbyDecision checks the resolved descriptor carries a standby
// record exactly when the blueprint deploys one. Both values are checked because
// a resolver that ignored the decision would still produce a descriptor that
// looks right for one of them.
func TestBuildCopiesStandbyDecision(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("disabled=%t", disabled), func(t *testing.T) {
			p := projectOf(site(machine("sensor", "10.0.1.10", disabled), companion()))
			plan, err := resolve.Build(p, "acme-opdl")
			require.NoError(t, err)
			require.Equal(t, !disabled, plan.Machines[0].HasStandby())
			require.NotEmpty(t, plan.Machines[0].Primary.APIAddress)
		})
	}
}

// TestBuildStandbyIsPerMachine checks one machine's standby decision does not
// change another's, and that the result is independent of declaration order.
func TestBuildStandbyIsPerMachine(t *testing.T) {
	build := func(machines []blueprint.Machine) *resolve.Plan {
		plan, err := resolve.Build(projectOf(site(machines...)), "acme-opdl")
		require.NoError(t, err)
		return plan
	}

	enabled := machine("sensor", "10.0.1.10", false)
	disabled := machine("gateway", "10.0.1.11", true)

	forward := build([]blueprint.Machine{enabled, disabled})
	require.True(t, machineByName(t, forward, "sensor").HasStandby())
	require.False(t, machineByName(t, forward, "gateway").HasStandby())

	// Declaration order must not change the resolved policy of either machine.
	reversed := build([]blueprint.Machine{disabled, enabled})
	require.True(t, machineByName(t, reversed, "sensor").HasStandby())
	require.False(t, machineByName(t, reversed, "gateway").HasStandby())
}

func TestBuildValidatesBlueprint(t *testing.T) {
	p := project()
	p.Sites[0].Machines[0].Standby = nil
	_, err := resolve.Build(p, "acme-opdl")
	require.ErrorContains(t, err, "standby block is required")
}

func TestBuildValidatesDescriptors(t *testing.T) {
	p := project()
	p.Sites[0].Machines[0].IP = "not-an-ip"
	_, err := resolve.Build(p, "acme-opdl")
	require.ErrorContains(t, err, "not a valid IP address")
}

func TestBuildRequiresPlatformName(t *testing.T) {
	_, err := resolve.Build(project(), "")
	require.ErrorContains(t, err, "platform is required")
}

// TestBuildResolvesTheSiteEventFabric checks what a site's authored cluster name
// and its instances' authored ports become: one cluster, one route listener per
// instance on that instance's machine ip, and a route from every member to every
// other member of the site.
//
// The membership is the subject. A site of two machines and three instances is
// the smallest fixture where every distinction matters at once: routing to the
// other instance of one's own machine, routing across machines, and not routing
// to oneself.
func TestBuildResolvesTheSiteEventFabric(t *testing.T) {
	p := projectOf(site(
		machine("node-a", "10.0.1.10", false),
		machine("node-b", "10.0.1.11", true),
	))
	plan, err := resolve.Build(p, "acme-opdl")
	require.NoError(t, err)

	nodeA := machineByName(t, plan, "node-a")
	nodeB := machineByName(t, plan, "node-b")

	// Every server names the site's cluster. It is what a server checks before
	// it accepts a route, so a member that named a different one would bind its
	// listener and join nothing.
	for _, nats := range []deployment.NATS{nodeA.Primary.NATS, nodeA.Standby.NATS, nodeB.Primary.NATS} {
		require.Equal(t, siteClusterName, nats.ClusterName)
	}

	// The listeners are on each machine's own ip, not on loopback. A site's
	// cluster spans machines, so a member has to be reachable from another host.
	require.Equal(t, "10.0.1.10:6222", nodeA.Primary.NATS.ClusterAddress)
	require.Equal(t, "10.0.1.10:6223", nodeA.Standby.NATS.ClusterAddress)
	require.Equal(t, "10.0.1.11:6222", nodeB.Primary.NATS.ClusterAddress)

	// Each member routes to the other two and never to itself.
	require.Equal(t, []string{"nats://10.0.1.10:6223", "nats://10.0.1.11:6222"}, nodeA.Primary.NATS.Routes)
	require.Equal(t, []string{"nats://10.0.1.10:6222", "nats://10.0.1.11:6222"}, nodeA.Standby.NATS.Routes)
	require.Equal(t, []string{"nats://10.0.1.10:6222", "nats://10.0.1.10:6223"}, nodeB.Primary.NATS.Routes)
}

// TestBuildResolvesNoRoutesForASiteOfOneInstance checks the one member with
// nobody to route to. It still runs its own server; there is simply no peer, and
// an empty list says so.
func TestBuildResolvesNoRoutesForASiteOfOneInstance(t *testing.T) {
	plan, err := resolve.Build(projectOf(site(machine("solo", "10.0.1.10", true))), "acme-opdl")
	require.NoError(t, err)
	require.Empty(t, machineByName(t, plan, "solo").Primary.NATS.Routes)
}

// TestBuildCarriesTheMachineStore checks the machine's own event store reaches
// the descriptor on every machine, with or without a standby: a machine's facts
// are the machine's whether or not a second instance exists to read them.
func TestBuildCarriesTheMachineStore(t *testing.T) {
	for name, standbyDisabled := range map[string]bool{"standby deployed": false, "standby disabled": true} {
		t.Run(name, func(t *testing.T) {
			p := projectOf(site(machine("sensor", "10.0.1.10", standbyDisabled), companion()))
			plan, err := resolve.Build(p, "acme-opdl")
			require.NoError(t, err)
			require.Equal(t, machineEventsFile("sensor"), plan.Machines[0].MachineEventsFile)
		})
	}
}

// TestBuildCarriesAuthoredLock checks the lock policy is carried onto the descriptor when
// a standby is deployed, and nil when standby is disabled.
func TestBuildCarriesAuthoredLease(t *testing.T) {
	t.Run("standby deployed carries lease", func(t *testing.T) {
		p := projectOf(site(machine("sensor", "10.0.1.10", false), companion()))
		plan, err := resolve.Build(p, "acme-opdl")
		require.NoError(t, err)
		require.Equal(t, &deployment.Lease{
			File:                  "D:/opdl/sensor/lease",
			Duration:              "15s",
			RenewalInterval:       "5s",
			HealthCheckInterval:   "2s",
			FailbackStabilization: "30s",
			LagBound:              "30s",
		}, plan.Machines[0].Lease)
	})

	t.Run("standby disabled yields nil lease", func(t *testing.T) {
		p := projectOf(site(machine("sensor", "10.0.1.10", true), companion()))
		plan, err := resolve.Build(p, "acme-opdl")
		require.NoError(t, err)
		require.Nil(t, plan.Machines[0].Lease)
	})
}

// TestBuildCarriesWinServiceIdentities checks the authored service names reach
// the descriptor, with the display name default filled in, and that an undeployed
// instance carries none.
func TestBuildCarriesWinServiceIdentities(t *testing.T) {
	t.Run("standby deployed", func(t *testing.T) {
		p := projectOf(site(machine("node-a", "10.0.1.10", false), companion()))
		plan, err := resolve.Build(p, "acme-opdl")
		require.NoError(t, err)

		m := plan.Machines[0]
		require.NotNil(t, m.Primary.Service)
		require.Equal(t, "node-a-primary", m.Primary.Service.Name)
		require.Equal(t, "node-a-primary", m.Primary.Service.DisplayName, "display name defaults to the name")
		require.NotNil(t, m.Standby)
		require.Equal(t, "node-a-standby", m.Standby.Service.Name)
	})

	t.Run("standby not deployed", func(t *testing.T) {
		p := projectOf(site(machine("node-a", "10.0.1.10", true), companion()))
		plan, err := resolve.Build(p, "acme-opdl")
		require.NoError(t, err)

		m := plan.Machines[0]
		require.NotNil(t, m.Primary.Service, "a machine always deploys a Primary Instance")
		require.Nil(t, m.Standby, "an undeployed instance has no record at all")
	})
}
