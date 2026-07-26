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
		}
		standby.DataDir = dataDir(name, "standby")
		standby.API = &blueprint.API{LocalPort: standbyAPIPort}
		standby.WinService = &blueprint.WinService{Name: name + "-standby"}
	}
	return blueprint.Machine{
		Name: name, MachineProfile: "node", IP: ip, Services: []string{"core-services"},
		Platform: &blueprint.Platform{
			DataDir:    dataDir(name, "primary"),
			API:        &blueprint.API{LocalPort: apiPort},
			WinService: &blueprint.WinService{Name: name + "-primary"},
			Standby:    standby,
		},
	}
}

// dataDir is one instance's general platform data root.
func dataDir(machine, role string) string {
	return fmt.Sprintf("D:/opdl-data/%s/%s", machine, role)
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
	require.False(t, m.Instances.Primary.Disabled, "a machine always deploys a primary process")
}

// TestBuildCopiesStandbyDecision checks the resolved descriptor states the
// blueprint's standby decision rather than implying it. Both values are checked
// because the field is a bool: a resolver that dropped it would still produce a
// descriptor that looks right for one of them.
func TestBuildCopiesStandbyDecision(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("disabled=%t", disabled), func(t *testing.T) {
			p := projectOf(blueprint.Site{
				Name:     "north",
				Machines: []blueprint.Machine{machine("sensor", "10.0.1.10", disabled), companion()},
			})
			plan, err := resolve.Build(p, "acme-opdl")
			require.NoError(t, err)
			require.Equal(t, disabled, plan.Machines[0].Instances.Standby.Disabled)
			require.False(t, plan.Machines[0].Instances.Primary.Disabled)
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
	require.False(t, machineByName(t, forward, "sensor").Instances.Standby.Disabled)
	require.True(t, machineByName(t, forward, "gateway").Instances.Standby.Disabled)

	// Declaration order must not change the resolved policy of either machine.
	reversed := build([]blueprint.Machine{disabled, enabled})
	require.False(t, machineByName(t, reversed, "sensor").Instances.Standby.Disabled)
	require.True(t, machineByName(t, reversed, "gateway").Instances.Standby.Disabled)
}

func TestBuildValidatesBlueprint(t *testing.T) {
	p := project()
	p.Sites[0].Machines[0].Platform.Standby = nil
	_, err := resolve.Build(p, "acme-opdl")
	require.ErrorContains(t, err, "platform.standby block is required")
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

		instances := plan.Machines[0].Instances
		require.NotNil(t, instances.Primary.Service)
		require.Equal(t, "node-a-primary", instances.Primary.Service.Name)
		require.Equal(t, "node-a-primary", instances.Primary.Service.DisplayName, "display name defaults to the name")
		require.NotNil(t, instances.Standby.Service)
		require.Equal(t, "node-a-standby", instances.Standby.Service.Name)
	})

	t.Run("standby not deployed", func(t *testing.T) {
		p := projectOf(blueprint.Site{Name: "north", Machines: []blueprint.Machine{machine("node-a", "10.0.1.10", true), companion()}})
		plan, err := resolve.Build(p, "acme-opdl")
		require.NoError(t, err)

		instances := plan.Machines[0].Instances
		require.NotNil(t, instances.Primary.Service, "a machine always deploys a Primary Instance")
		require.Nil(t, instances.Standby.Service, "an undeployed instance names no service")
	})
}
