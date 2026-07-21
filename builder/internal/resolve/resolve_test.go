package resolve_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/builder/deployment"
	"github.com/miroslav-matejovsky/opdl/builder/internal/blueprint"
	"github.com/miroslav-matejovsky/opdl/builder/internal/resolve"
)

// Every machine in these fixtures authors the same two ports. That is the point
// of the contract: ports are a machine-level policy, and the addresses that
// distinguish machines come from their ips, not from giving each one a different
// port.
const (
	clientPort  = 4222
	clusterPort = 6222
)

// machine builds a valid machine with the mandatory platform policy filled in.
func machine(name, ip string, standbyDisabled bool) blueprint.Machine {
	standby := &blueprint.Standby{Disabled: standbyDisabled}
	if !standbyDisabled {
		standby.WinService = &blueprint.WinService{Name: name + "-standby"}
	}
	return blueprint.Machine{
		Name: name, Role: "node", IP: ip, Services: []string{"core-services"},
		Platform: &blueprint.Platform{
			WinService: &blueprint.WinService{Name: name + "-primary"},
			Nats:       &blueprint.Nats{ClientPort: clientPort, ClusterPort: clusterPort},
			Standby:    standby,
		},
	}
}

// site builds a site of machinesCount machines named node-1..node-N with
// sequential ips, declared in reverse name order so a test can tell derived
// ordering from declaration order.
func site(name string, machineCount int) blueprint.Site {
	machines := make([]blueprint.Machine, 0, machineCount)
	for i := machineCount; i >= 1; i-- {
		machines = append(machines, machine(fmt.Sprintf("node-%d", i), fmt.Sprintf("10.0.1.%d", i), true))
	}
	return blueprint.Site{Name: name, Machines: machines}
}

func projectOf(sites ...blueprint.Site) *blueprint.Project {
	return &blueprint.Project{
		Name:        "customer-a",
		Environment: "production",
		Features:    blueprint.Features{Chaos: true},
		Sites:       sites,
	}
}

// project is a one-machine, one-site project.
func project() *blueprint.Project {
	return projectOf(blueprint.Site{
		Name: "north",
		Machines: []blueprint.Machine{{
			Name:     "sensor",
			Role:     "sensor-node",
			IP:       "10.0.1.10",
			Services: []string{"sensor-services"},
			Platform: &blueprint.Platform{
				WinService: &blueprint.WinService{Name: "sensor-primary"},
				Nats:       &blueprint.Nats{ClientPort: clientPort, ClusterPort: clusterPort},
				Standby:    &blueprint.Standby{Disabled: true},
			},
		}},
	})
}

// twoSiteProject has two sites, and declares machines out of name order so a
// test can tell derived ordering from declaration order.
func twoSiteProject() *blueprint.Project {
	return projectOf(
		blueprint.Site{Name: "north", Machines: []blueprint.Machine{
			machine("sensor", "10.0.1.10", true),
			machine("gateway", "10.0.1.11", true),
			machine("archive", "10.0.1.12", true),
		}},
		blueprint.Site{Name: "south", Machines: []blueprint.Machine{
			machine("south-node", "10.0.2.10", true),
		}},
	)
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
	require.False(t, m.Slots.Primary.Disabled, "a machine always deploys a primary process")
}

// TestBuildCopiesStandbyDecision checks the resolved descriptor states the
// blueprint's standby decision rather than implying it. Both values are checked
// because the field is a bool: a resolver that dropped it would still produce a
// descriptor that looks right for one of them.
func TestBuildCopiesStandbyDecision(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("disabled=%t", disabled), func(t *testing.T) {
			p := project()
			p.Sites[0].Machines[0].Platform.Standby.Disabled = disabled
			if !disabled {
				// A deployed standby must name its own service.
				p.Sites[0].Machines[0].Platform.Standby.WinService = &blueprint.WinService{Name: "sensor-standby"}
			}
			plan, err := resolve.Build(p, "acme-opdl")
			require.NoError(t, err)
			require.Equal(t, disabled, plan.Machines[0].Slots.Standby.Disabled)
			require.False(t, plan.Machines[0].Slots.Primary.Disabled)
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
	require.False(t, machineByName(t, forward, "sensor").Slots.Standby.Disabled)
	require.True(t, machineByName(t, forward, "gateway").Slots.Standby.Disabled)

	// Declaration order must not change the resolved policy of either machine.
	reversed := build([]blueprint.Machine{disabled, enabled})
	require.False(t, machineByName(t, reversed, "sensor").Slots.Standby.Disabled)
	require.True(t, machineByName(t, reversed, "gateway").Slots.Standby.Disabled)
}

// TestBuildDerivesOneMemberEventFabricForSingleMachineSite checks a standalone
// machine is a valid fabric of one, not a machine with a missing fabric.
func TestBuildDerivesOneMemberEventFabricForSingleMachineSite(t *testing.T) {
	plan, err := resolve.Build(project(), "acme-opdl")
	require.NoError(t, err)
	require.Equal(t, deployment.EventFabric{
		Nats: deployment.EventFabricNats{
			ClientAddress:  "10.0.1.10:4222",
			ClusterAddress: "10.0.1.10:6222",
			Routes:         []string{},
			Servers:        []string{"10.0.1.10:4222"},
		},
		Peers: []deployment.EventFabricPeer{},
	}, plan.Machines[0].EventFabric)
}

// TestBuildDerivesEventFabricPeersFromTheSiteOnly checks the fabric spans exactly one
// site: a machine's peers are its site's other machines, never another site's,
// and every machine of a site sees the same membership.
func TestBuildDerivesEventFabricPeersFromTheSiteOnly(t *testing.T) {
	plan, err := resolve.Build(twoSiteProject(), "acme-opdl")
	require.NoError(t, err)

	sensor := machineByName(t, plan, "sensor")
	require.Equal(t, []deployment.EventFabricPeer{
		{Site: "north", Machine: "archive", IP: "10.0.1.12"},
		{Site: "north", Machine: "gateway", IP: "10.0.1.11"},
	}, sensor.EventFabric.Peers, "peers are the site's other machines, ordered by name")

	// The site has three machines, so all three store the journal and route to
	// each other. Storage order is by machine name: archive, gateway, sensor.
	require.Equal(t, deployment.EventFabricNats{
		ClientAddress:  "10.0.1.10:4222",
		ClusterAddress: "10.0.1.10:6222",
		Servers:        []string{"10.0.1.10:4222", "10.0.1.12:4222", "10.0.1.11:4222"},
		Routes:         []string{"10.0.1.12:6222", "10.0.1.11:6222"},
	}, sensor.EventFabric.Nats)

	// The south machine is alone in its site, so it forms its own fabric and
	// never meets the north machines.
	south := machineByName(t, plan, "south-node")
	require.Empty(t, south.EventFabric.Peers)
	require.Empty(t, south.EventFabric.Nats.Routes)
}

// TestBuildDerivesStorageTopologyBySiteSize checks the listener matrix the
// contract promises: how many machines store the journal, which of them bind a
// cluster listener, and what a machine that stores nothing still knows.
//
// The cluster listener is the interesting column. A one or two machine site
// authors a cluster port like every other machine but must resolve no routes,
// because there is no peer server to route to and binding it would open a port
// nothing can reach.
func TestBuildDerivesStorageTopologyBySiteSize(t *testing.T) {
	tests := []struct {
		siteSize     int
		storage      []string
		wantRoutes   map[string]int
		wantServers  int
		clusterNodes int
	}{
		{siteSize: 1, storage: []string{"node-1"}, wantServers: 1, clusterNodes: 0},
		{siteSize: 2, storage: []string{"node-1"}, wantServers: 1, clusterNodes: 0},
		{siteSize: 3, storage: []string{"node-1", "node-2", "node-3"}, wantServers: 3, clusterNodes: 3},
		{siteSize: 4, storage: []string{"node-1", "node-2", "node-3"}, wantServers: 3, clusterNodes: 3},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%d machines", tc.siteSize), func(t *testing.T) {
			plan, err := resolve.Build(projectOf(site("north", tc.siteSize)), "acme-opdl")
			require.NoError(t, err)
			require.Len(t, plan.Machines, tc.siteSize)

			withRoutes := 0
			for _, d := range plan.Machines {
				nats := d.EventFabric.Nats
				storesJournal := slices.Contains(tc.storage, d.Machine)

				// Every machine reaches the journal through the same storage
				// servers, whether or not it hosts one.
				require.Len(t, nats.Servers, tc.wantServers, "machine %s", d.Machine)
				if storesJournal {
					require.Equal(t, nats.ClientAddress, nats.Servers[0],
						"machine %s stores the journal and must connect to itself first", d.Machine)
				} else {
					require.Empty(t, nats.Routes,
						"machine %s stores nothing and must not route", d.Machine)
					require.NotContains(t, nats.Servers, nats.ClientAddress,
						"machine %s stores nothing, so its own address is not a server", d.Machine)
				}
				if len(nats.Routes) > 0 {
					withRoutes++
					require.Len(t, nats.Routes, len(tc.storage)-1, "machine %s", d.Machine)
					require.NotContains(t, nats.Routes, nats.ClusterAddress,
						"machine %s must not route to itself", d.Machine)
				}
				// The addresses are always resolved, even where nothing binds
				// them, so a descriptor reader never has to guess them.
				require.NotEmpty(t, nats.ClientAddress)
				require.NotEmpty(t, nats.ClusterAddress)
			}
			require.Equal(t, tc.clusterNodes, withRoutes, "machines that bind a cluster listener")
		})
	}
}

// TestBuildEventFabricOrderIsIndependentOfDeclarationOrder checks the derived
// topology is a function of the machines, not of how the blueprint was authored.
func TestBuildEventFabricOrderIsIndependentOfDeclarationOrder(t *testing.T) {
	authored, err := resolve.Build(twoSiteProject(), "acme-opdl")
	require.NoError(t, err)

	reversed := twoSiteProject()
	slices.Reverse(reversed.Sites[0].Machines)
	shuffled, err := resolve.Build(reversed, "acme-opdl")
	require.NoError(t, err)

	for _, name := range []string{"sensor", "gateway", "archive"} {
		require.Equal(t,
			machineByName(t, authored, name).EventFabric,
			machineByName(t, shuffled, name).EventFabric,
			"machine %s", name)
	}
}

// TestBuildResolvesEmptyListsAsEmptyArrays checks an empty route list is an
// empty JSON array rather than null, so a descriptor reader can tell "resolved
// to nothing" from "not resolved".
func TestBuildResolvesEmptyListsAsEmptyArrays(t *testing.T) {
	plan, err := resolve.Build(project(), "acme-opdl")
	require.NoError(t, err)
	require.NotNil(t, plan.Machines[0].EventFabric.Nats.Routes)
	require.Empty(t, plan.Machines[0].EventFabric.Nats.Routes)
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

// TestBuildDerivesFenceObject checks the ownership object name is derived rather
// than authored, and that it is stable: the same machine must resolve the same
// object on every build or a rebuilt package would stop excluding its peer.
func TestBuildDerivesFenceObject(t *testing.T) {
	plan, err := resolve.Build(project(), "acme-opdl")
	require.NoError(t, err)
	object := plan.Machines[0].Fence.Object

	require.True(t, strings.HasPrefix(object, blueprint.DefaultFenceNamespace+".fence."), "object %q", object)
	require.NotContains(t, object, `\`, "the kernel namespace is applied by the platform, not the builder")

	again, err := resolve.Build(project(), "acme-opdl")
	require.NoError(t, err)
	require.Equal(t, object, again.Machines[0].Fence.Object, "the same machine must derive the same object")
}

// TestBuildFenceObjectIsUniquePerMachineIdentity is the property that makes the
// fence safe to derive. Two machines sharing an object would contend for each
// other's ownership, and the descriptor would look correct.
func TestBuildFenceObjectIsUniquePerMachineIdentity(t *testing.T) {
	base := project()
	basePlan, err := resolve.Build(base, "acme-opdl")
	require.NoError(t, err)
	object := basePlan.Machines[0].Fence.Object

	for name, mutate := range map[string]func(*blueprint.Project){
		"machine":     func(p *blueprint.Project) { p.Sites[0].Machines[0].Name = "other" },
		"site":        func(p *blueprint.Project) { p.Sites[0].Name = "south" },
		"project":     func(p *blueprint.Project) { p.Name = "customer-b" },
		"environment": func(p *blueprint.Project) { p.Environment = "staging" },
	} {
		t.Run(name, func(t *testing.T) {
			p := project()
			mutate(p)
			plan, err := resolve.Build(p, "acme-opdl")
			require.NoError(t, err)
			require.NotEqual(t, object, plan.Machines[0].Fence.Object)
		})
	}
}

// TestBuildFenceObjectIgnoresNonIdentityChanges checks the object does not move
// when a machine is re-addressed. An ownership object that changed with the IP
// would let a machine's old and new processes both become active.
func TestBuildFenceObjectIgnoresNonIdentityChanges(t *testing.T) {
	basePlan, err := resolve.Build(project(), "acme-opdl")
	require.NoError(t, err)

	p := project()
	p.Sites[0].Machines[0].IP = "10.9.9.9"
	p.Sites[0].Machines[0].Role = "another-role"
	plan, err := resolve.Build(p, "acme-opdl")
	require.NoError(t, err)

	require.Equal(t, basePlan.Machines[0].Fence.Object, plan.Machines[0].Fence.Object)
}

// TestBuildFenceNamespaceIsAuthored checks the one part a blueprint controls
// reaches the derived name, so two deployments of the same identity on one host
// can be told apart deliberately.
func TestBuildFenceNamespaceIsAuthored(t *testing.T) {
	defaultPlan, err := resolve.Build(project(), "acme-opdl")
	require.NoError(t, err)

	p := project()
	p.Sites[0].Machines[0].Platform.Fence = &blueprint.Fence{Namespace: "rig-b"}
	plan, err := resolve.Build(p, "acme-opdl")
	require.NoError(t, err)

	require.True(t, strings.HasPrefix(plan.Machines[0].Fence.Object, "rig-b.fence."))
	require.NotEqual(t, defaultPlan.Machines[0].Fence.Object, plan.Machines[0].Fence.Object)
}

// TestBuildCarriesWinServiceIdentities checks the authored service names reach
// the descriptor, with the display name default filled in, and that an undeployed
// instance carries none.
func TestBuildCarriesWinServiceIdentities(t *testing.T) {
	t.Run("standby deployed", func(t *testing.T) {
		p := projectOf(blueprint.Site{Name: "north", Machines: []blueprint.Machine{machine("node-a", "10.0.1.10", false)}})
		plan, err := resolve.Build(p, "acme-opdl")
		require.NoError(t, err)

		slots := plan.Machines[0].Slots
		require.NotNil(t, slots.Primary.Service)
		require.Equal(t, "node-a-primary", slots.Primary.Service.Name)
		require.Equal(t, "node-a-primary", slots.Primary.Service.DisplayName, "display name defaults to the name")
		require.NotNil(t, slots.Standby.Service)
		require.Equal(t, "node-a-standby", slots.Standby.Service.Name)
	})

	t.Run("standby not deployed", func(t *testing.T) {
		p := projectOf(blueprint.Site{Name: "north", Machines: []blueprint.Machine{machine("node-a", "10.0.1.10", true)}})
		plan, err := resolve.Build(p, "acme-opdl")
		require.NoError(t, err)

		slots := plan.Machines[0].Slots
		require.NotNil(t, slots.Primary.Service, "a machine always deploys a Primary Instance")
		require.Nil(t, slots.Standby.Service, "an undeployed instance names no service")
	})
}
