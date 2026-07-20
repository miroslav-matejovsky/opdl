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
			Platform: &blueprint.Platform{
				Nats: &blueprint.Nats{
					ClientAddress:  ip + ":4222",
					ClusterAddress: ip + ":6222",
					MonitorAddress: "127.0.0.1:8222",
				},
			},
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
				Platform: &blueprint.Platform{
					Nats: &blueprint.Nats{
						ClientAddress:  "10.0.1.10:4222",
						ClusterAddress: "10.0.1.10:6222",
						MonitorAddress: "127.0.0.1:8222",
					},
				},
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
	// Standby slot is not enabled when standby block is omitted from platform.
	require.NotNil(t, m.Slots.Primary)
	require.Nil(t, m.Slots.Standby)
}

func natsFor(ip string) *blueprint.Nats {
	return &blueprint.Nats{
		ClientAddress:  ip + ":4222",
		ClusterAddress: ip + ":6222",
		MonitorAddress: "127.0.0.1:8222",
	}
}

func standbyFor(ip string) *blueprint.Standby {
	return &blueprint.Standby{
		Nats: &blueprint.Nats{
			ClientAddress:  ip + ":4223",
			ClusterAddress: ip + ":6223",
			MonitorAddress: "127.0.0.1:8223",
		},
	}
}

// TestBuildStandbyPolicy checks standby is enabled only when standby block is explicitly provided.
func TestBuildStandbyPolicy(t *testing.T) {
	cases := map[string]struct {
		platform *blueprint.Platform
		want     bool
	}{
		"omitted standby":  {platform: &blueprint.Platform{Nats: natsFor("10.0.1.10")}, want: false},
		"explicit standby": {platform: &blueprint.Platform{Nats: natsFor("10.0.1.10"), Standby: standbyFor("10.0.1.10")}, want: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p := project()
			p.Sites[0].Machines[0].Platform = tc.platform
			plan, err := resolve.Build(p, "acme-opdl")
			require.NoError(t, err)
			require.Equal(t, tc.want, plan.Machines[0].Slots.Standby != nil)
		})
	}
}

// TestBuildStandbyIsPerMachine checks one machine opting into standby does not change
// another's policy, and that the result is independent of declaration order.
func TestBuildStandbyIsPerMachine(t *testing.T) {
	build := func(machines []blueprint.Machine) *resolve.Plan {
		p := &blueprint.Project{
			Name:        "customer-a",
			Environment: "production",
			Sites:       []blueprint.Site{{Name: "north", Machines: machines}},
		}
		plan, err := resolve.Build(p, "acme-opdl")
		require.NoError(t, err)
		return plan
	}

	withStandby := blueprint.Machine{
		Name: "sensor", Role: "node", IP: "10.0.1.10", Services: []string{"core-services"},
		Platform: &blueprint.Platform{Nats: natsFor("10.0.1.10"), Standby: standbyFor("10.0.1.10")},
	}
	def := blueprint.Machine{
		Name: "gateway", Role: "node", IP: "10.0.1.11", Services: []string{"core-services"},
		Platform: &blueprint.Platform{Nats: natsFor("10.0.1.11")},
	}

	forward := build([]blueprint.Machine{withStandby, def})
	require.NotNil(t, machineByName(t, forward, "sensor").Slots.Standby)
	require.Nil(t, machineByName(t, forward, "gateway").Slots.Standby)

	// Declaration order must not change the resolved policy of either machine.
	reversed := build([]blueprint.Machine{def, withStandby})
	require.NotNil(t, machineByName(t, reversed, "sensor").Slots.Standby)
	require.Nil(t, machineByName(t, reversed, "gateway").Slots.Standby)
}

// TestBuildDerivesOneMemberEventFabricForSingleMachineSite checks a standalone
// machine is a valid fabric of one, not a machine with a missing fabric.
func TestBuildDerivesOneMemberEventFabricForSingleMachineSite(t *testing.T) {
	plan, err := resolve.Build(project(), "acme-opdl")
	require.NoError(t, err)
	require.Equal(t, deployment.EventFabric{
		Peers: []deployment.EventFabricPeer{},
	}, plan.Machines[0].EventFabric)
	require.Equal(t, deployment.EventFabricNats{
		ClientAddress:  "10.0.1.10:4222",
		ClusterAddress: "10.0.1.10:6222",
		MonitorAddress: "127.0.0.1:8222",
		Servers:        []string{"10.0.1.10:4222"},
	}, plan.Machines[0].Slots.Primary.EventFabric.Nats)
}

// TestBuildDerivesEventFabricPeersFromTheSiteOnly checks the fabric spans exactly one
// site: a machine's peers are its site's other machines, never another site's,
// and every machine of a site sees the same membership.
func TestBuildDerivesEventFabricPeersFromTheSiteOnly(t *testing.T) {
	plan, err := resolve.Build(twoSiteProject(), "acme-opdl")
	require.NoError(t, err)

	sensor := machineByName(t, plan, "sensor")
	require.Equal(t, deployment.EventFabric{
		Peers: []deployment.EventFabricPeer{
			{Site: "north", Machine: "archive", IP: "10.0.1.12"},
			{Site: "north", Machine: "gateway", IP: "10.0.1.11"},
		},
	}, sensor.EventFabric, "peers are the site's other machines, ordered by name")
	require.Equal(t, deployment.EventFabricNats{
		ClientAddress:  "10.0.1.10:4222",
		ClusterAddress: "10.0.1.10:6222",
		MonitorAddress: "127.0.0.1:8222",
		Servers:        []string{"10.0.1.10:4222", "10.0.1.12:4222", "10.0.1.11:4222"},
		Routes:         []string{"10.0.1.12:6222", "10.0.1.11:6222"},
	}, sensor.Slots.Primary.EventFabric.Nats)

	// The south machine is alone in its site, so it forms its own fabric and
	// never meets the north machines.
	south := machineByName(t, plan, "south-node")
	require.Empty(t, south.EventFabric.Peers)
}

// TestBuildEventFabricOrderIsIndependentOfDeclarationOrder checks the derived
// topology is a function of the machines, not of how the blueprint was authored.
func TestBuildEventFabricOrderIsIndependentOfDeclarationOrder(t *testing.T) {
	authored, err := resolve.Build(twoSiteProject(), "acme-opdl")
	require.NoError(t, err)

	reversed := twoSiteProject()
	north := reversed.Sites[0].Machines
	slices.Reverse(north)
	shuffled, err := resolve.Build(reversed, "acme-opdl")
	require.NoError(t, err)

	require.Equal(t,
		machineByName(t, authored, "sensor").EventFabric,
		machineByName(t, shuffled, "sensor").EventFabric)
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

func TestBuildHonorsExplicitNatsRoutesAndServers(t *testing.T) {
	p := project()
	p.Sites[0].Machines[0].Platform.Nats.Routes = []string{"10.0.1.99:6222"}
	p.Sites[0].Machines[0].Platform.Nats.Servers = []string{"10.0.1.10:4222", "10.0.1.99:4222"}
	plan, err := resolve.Build(p, "acme-opdl")
	require.NoError(t, err)
	require.Equal(t, []string{"10.0.1.99:6222"}, plan.Machines[0].Slots.Primary.EventFabric.Nats.Routes)
	require.Equal(t, []string{"10.0.1.10:4222", "10.0.1.99:4222"}, plan.Machines[0].Slots.Primary.EventFabric.Nats.Servers)
}
