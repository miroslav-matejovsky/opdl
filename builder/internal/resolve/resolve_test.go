package resolve_test

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/builder/deployment"
	"github.com/miroslav-matejovsky/opdl/builder/internal/blueprint"
	"github.com/miroslav-matejovsky/opdl/builder/internal/resolve"
)

// twoSiteProject has two sites, and declares machines out of name order so a
// test can tell derived ordering from declaration order.
func twoSiteProject() *blueprint.Project {
	machine := func(name, ip string) blueprint.Machine {
		return blueprint.Machine{
			Name: name, Role: "node", IP: ip, Services: []string{"core-services"},
			Platform: []blueprint.Platform{{Instances: []blueprint.PlatformInstance{
				{Name: "primary", APIAddress: ip + ":8080", FabricClientAddress: ip + ":3320", FabricMemberlistAddress: ip + ":3322"},
				{Name: "secondary", APIAddress: ip + ":8081", FabricClientAddress: ip + ":3321", FabricMemberlistAddress: ip + ":3323"},
			}}},
		}
	}
	return &blueprint.Project{
		Name:        "customer-a",
		Environment: "production",
		Sites: []blueprint.Site{
			{Name: "north", Machines: []blueprint.Machine{
				machine("sensor", "10.0.1.10"),
				machine("gateway", "10.0.1.11"),
				machine("archive", "10.0.1.12"),
			}},
			{Name: "south", Machines: []blueprint.Machine{
				machine("south-node", "10.0.2.10"),
			}},
		},
	}
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

func project() *blueprint.Project {
	return &blueprint.Project{
		Name:        "customer-a",
		Environment: "production",
		Features:    blueprint.Features{Chaos: true},
		Sites: []blueprint.Site{{
			Name: "north",
			Machines: []blueprint.Machine{{
				Name:     "sensor",
				Role:     "sensor-node",
				IP:       "10.0.1.10",
				Services: []string{"sensor-services"},
				Platform: []blueprint.Platform{{Instances: []blueprint.PlatformInstance{
					{Name: "primary", APIAddress: "10.0.1.10:8080", FabricClientAddress: "10.0.1.10:3320", FabricMemberlistAddress: "10.0.1.10:3322"},
					{Name: "secondary", APIAddress: "10.0.1.10:8081", FabricClientAddress: "10.0.1.10:3321", FabricMemberlistAddress: "10.0.1.10:3323"},
				}}},
			}},
		}},
	}
}

func TestBuildProducesMachineDescriptors(t *testing.T) {
	plan, err := resolve.Build(project(), "acme-opdl")
	require.NoError(t, err)
	require.Equal(t, "customer-a", plan.Project)
	require.Len(t, plan.Machines, 1)

	m := plan.Machines[0]
	require.Equal(t, "acme-opdl", m.Platform)
	require.Equal(t, "production", m.Environment)
	require.Equal(t, "north", m.Site)
	require.Equal(t, "sensor", m.Machine)
	require.Equal(t, "sensor-node", m.Role)
	require.Equal(t, "10.0.1.10", m.IP)
	require.Equal(t, []string{"sensor-services"}, m.Services)
	require.True(t, m.Features.Chaos)
	require.Equal(t, []deployment.PlatformInstance{
		{Name: "primary", APIAddress: "10.0.1.10:8080", FabricClientAddress: "10.0.1.10:3320", FabricMemberlistAddress: "10.0.1.10:3322"},
		{Name: "secondary", APIAddress: "10.0.1.10:8081", FabricClientAddress: "10.0.1.10:3321", FabricMemberlistAddress: "10.0.1.10:3323"},
	}, m.PlatformInstances)
}

// TestBuildDerivesOneMemberFabricForSingleMachineSite checks a standalone
// machine is a valid fabric of one, not a machine with a missing fabric.
func TestBuildDerivesOneMemberFabricForSingleMachineSite(t *testing.T) {
	plan, err := resolve.Build(project(), "acme-opdl")
	require.NoError(t, err)
	require.Equal(t, deployment.Fabric{
		Peers: []deployment.FabricPeer{},
	}, plan.Machines[0].Fabric)
}

// TestBuildDerivesFabricPeersFromTheSiteOnly checks the fabric spans exactly one
// site: a machine's peers are its site's other machines, never another site's,
// and every machine of a site sees the same membership.
func TestBuildDerivesFabricPeersFromTheSiteOnly(t *testing.T) {
	plan, err := resolve.Build(twoSiteProject(), "acme-opdl")
	require.NoError(t, err)

	sensor := machineByName(t, plan, "sensor")
	require.Equal(t, deployment.Fabric{
		Peers: []deployment.FabricPeer{
			{Site: "north", Machine: "archive", Instance: "primary", IP: "10.0.1.12", FabricClientAddress: "10.0.1.12:3320", FabricMemberlistAddress: "10.0.1.12:3322"},
			{Site: "north", Machine: "archive", Instance: "secondary", IP: "10.0.1.12", FabricClientAddress: "10.0.1.12:3321", FabricMemberlistAddress: "10.0.1.12:3323"},
			{Site: "north", Machine: "gateway", Instance: "primary", IP: "10.0.1.11", FabricClientAddress: "10.0.1.11:3320", FabricMemberlistAddress: "10.0.1.11:3322"},
			{Site: "north", Machine: "gateway", Instance: "secondary", IP: "10.0.1.11", FabricClientAddress: "10.0.1.11:3321", FabricMemberlistAddress: "10.0.1.11:3323"},
		},
	}, sensor.Fabric, "peers are the site's other machines, ordered by name")

	// The south machine is alone in its site, so it forms its own fabric and
	// never meets the north machines.
	south := machineByName(t, plan, "south-node")
	require.Empty(t, south.Fabric.Peers)
}

// TestBuildFabricOrderIsIndependentOfDeclarationOrder checks the derived
// topology is a function of the machines, not of how the blueprint was authored.
func TestBuildFabricOrderIsIndependentOfDeclarationOrder(t *testing.T) {
	authored, err := resolve.Build(twoSiteProject(), "acme-opdl")
	require.NoError(t, err)

	reversed := twoSiteProject()
	north := reversed.Sites[0].Machines
	slices.Reverse(north)
	shuffled, err := resolve.Build(reversed, "acme-opdl")
	require.NoError(t, err)

	require.Equal(t,
		machineByName(t, authored, "sensor").Fabric,
		machineByName(t, shuffled, "sensor").Fabric)
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

func TestBuildCanDisableSecondaryPerMachine(t *testing.T) {
	p := project()
	disabled := false
	p.Sites[0].Machines[0].Platform[0].SecondaryEnabled = &disabled
	p.Sites[0].Machines[0].Platform[0].Instances = p.Sites[0].Machines[0].Platform[0].Instances[:1]

	plan, err := resolve.Build(p, "acme-opdl")
	require.NoError(t, err)
	require.Equal(t, []deployment.PlatformInstance{
		{Name: "primary", APIAddress: "10.0.1.10:8080", FabricClientAddress: "10.0.1.10:3320", FabricMemberlistAddress: "10.0.1.10:3322"},
	}, plan.Machines[0].PlatformInstances)
}
