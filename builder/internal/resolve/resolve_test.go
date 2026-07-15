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
		return blueprint.Machine{Name: name, Role: "node", IP: ip, Services: []string{"core-services"}}
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
		Features:    blueprint.Features{Chaos: true, Redundancy: true},
		Sites: []blueprint.Site{{
			Name: "north",
			Machines: []blueprint.Machine{{
				Name:     "sensor",
				Role:     "sensor-node",
				IP:       "10.0.1.10",
				Services: []string{"sensor-services"},
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
	require.True(t, m.Features.Redundancy)
}

// TestBuildDerivesOneMemberFabricForSingleMachineSite checks a standalone
// machine is a valid fabric of one, not a machine with a missing fabric.
func TestBuildDerivesOneMemberFabricForSingleMachineSite(t *testing.T) {
	plan, err := resolve.Build(project(), "acme-opdl")
	require.NoError(t, err)
	require.Equal(t, deployment.Fabric{
		Machine: "sensor",
		IP:      "10.0.1.10",
		Peers:   []deployment.FabricPeer{},
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
		Machine: "sensor",
		IP:      "10.0.1.10",
		Peers: []deployment.FabricPeer{
			{Site: "north", Machine: "archive", IP: "10.0.1.12"},
			{Site: "north", Machine: "gateway", IP: "10.0.1.11"},
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
