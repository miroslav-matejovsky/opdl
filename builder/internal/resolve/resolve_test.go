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
)

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
		standby.API = &blueprint.API{LocalPort: standbyAPIPort, ReadHeaderTimeout: "5s", ShutdownTimeout: "10s"}
		standby.WinService = &blueprint.WinService{Name: name + "-standby"}
	}
	return blueprint.Machine{
		Name: name, MachineProfile: "node", IP: ip, Services: []string{"core-services"},
		EventstoreFile: machineEventsFile(name),
		Primary: &blueprint.Primary{
			EventlogFile: eventsFile(name, "primary"),
			StateFile:    stateFile(name, "primary"),
			API:          &blueprint.API{LocalPort: apiPort, ReadHeaderTimeout: "5s", ShutdownTimeout: "10s"},
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

func projectOf(sites ...blueprint.Site) *blueprint.Project {
	return &blueprint.Project{
		Name:        "customer-a",
		Environment: "production",
		Sites:       sites,
	}
}

// project is a one-machine, one-site project.
func project() *blueprint.Project {
	return projectOf(blueprint.Site{
		Name:     "north",
		Machines: []blueprint.Machine{sensor(), companion()},
	})
}

// sensor is a one-instance machine: it deploys no Standby Instance.
func sensor() blueprint.Machine {
	m := machine("sensor", "10.0.1.10", true)
	m.MachineProfile = "sensor-node"
	m.Services = []string{"sensor-services"}
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
}

// TestBuildCarriesInstanceFiles checks each deployed instance's own files reach
// its own record, and that an undeployed instance carries none. Both instances
// are checked because a resolver that read the primary's paths for both would
// produce a descriptor that looks complete and puts two runtimes on one file.
func TestBuildCarriesInstanceFiles(t *testing.T) {
	p := projectOf(blueprint.Site{Name: "north", Machines: []blueprint.Machine{machine("node-a", "10.0.1.10", false), companion()}})
	plan, err := resolve.Build(p, "acme-opdl")
	require.NoError(t, err)

	m := plan.Machines[0]
	require.Equal(t, eventsFile("node-a", "primary"), m.Primary.EventsFile)
	require.Equal(t, stateFile("node-a", "primary"), m.Primary.StateFile)
	require.NotNil(t, m.Standby)
	require.Equal(t, eventsFile("node-a", "standby"), m.Standby.EventsFile)
	require.Equal(t, stateFile("node-a", "standby"), m.Standby.StateFile)
}

// TestBuildCopiesStandbyDecision checks the resolved descriptor carries a standby
// record exactly when the blueprint deploys one. Both values are checked because
// a resolver that ignored the decision would still produce a descriptor that
// looks right for one of them.
func TestBuildCopiesStandbyDecision(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("disabled=%t", disabled), func(t *testing.T) {
			p := projectOf(blueprint.Site{
				Name:     "north",
				Machines: []blueprint.Machine{machine("sensor", "10.0.1.10", disabled), companion()},
			})
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
		plan, err := resolve.Build(projectOf(blueprint.Site{Name: "north", Machines: machines}), "acme-opdl")
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

// TestBuildCarriesTheMachineStore checks the machine's own event store reaches
// the descriptor on every machine, with or without a standby: a machine's facts
// are the machine's whether or not a second instance exists to read them.
func TestBuildCarriesTheMachineStore(t *testing.T) {
	for name, standbyDisabled := range map[string]bool{"standby deployed": false, "standby disabled": true} {
		t.Run(name, func(t *testing.T) {
			p := projectOf(blueprint.Site{
				Name:     "north",
				Machines: []blueprint.Machine{machine("sensor", "10.0.1.10", standbyDisabled), companion()},
			})
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
		p := projectOf(blueprint.Site{
			Name:     "north",
			Machines: []blueprint.Machine{machine("sensor", "10.0.1.10", false), companion()},
		})
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
		p := projectOf(blueprint.Site{
			Name:     "north",
			Machines: []blueprint.Machine{machine("sensor", "10.0.1.10", true), companion()},
		})
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
		p := projectOf(blueprint.Site{Name: "north", Machines: []blueprint.Machine{machine("node-a", "10.0.1.10", false), companion()}})
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
		p := projectOf(blueprint.Site{Name: "north", Machines: []blueprint.Machine{machine("node-a", "10.0.1.10", true), companion()}})
		plan, err := resolve.Build(p, "acme-opdl")
		require.NoError(t, err)

		m := plan.Machines[0]
		require.NotNil(t, m.Primary.Service, "a machine always deploys a Primary Instance")
		require.Nil(t, m.Standby, "an undeployed instance has no record at all")
	})
}
