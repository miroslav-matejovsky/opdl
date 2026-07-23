package resolve_test

import (
	"fmt"
	"slices"
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
	apiPort     = 8080
	clientPort  = 4222
	clusterPort = 6222

	standbyAPIPort     = 8081
	standbyClientPort  = 4322
	standbyClusterPort = 6322
)

func jetstreamStoreDir(machine, role string) string {
	return fmt.Sprintf("D:/opdl/%s/%s/eventfabric/nats", machine, role)
}

// machine builds a valid machine with the mandatory platform policy filled in.
func machine(name, ip string, standbyDisabled bool) blueprint.Machine {
	standby := &blueprint.Standby{Disabled: standbyDisabled}
	if !standbyDisabled {
		standby.Lock = &blueprint.Lock{WindowsMutex: "Global\\opdl-" + name}
		standby.RuntimeDir = runtimeDir(name, "standby")
		standby.DataDir = dataDir(name, "standby")
		standby.API = &blueprint.API{LocalPort: standbyAPIPort}
		standby.WinService = &blueprint.WinService{Name: name + "-standby"}
		standby.EventStorage = &blueprint.EventStorage{
			Nats: &blueprint.Nats{
				ClientPort:        standbyClientPort,
				ClusterPort:       standbyClusterPort,
				JetStreamStoreDir: jetstreamStoreDir(name, "standby"),
			},
		}
	}
	return blueprint.Machine{
		Name: name, MachineProfile: "node", IP: ip, Services: []string{"core-services"},
		Platform: &blueprint.Platform{
			RuntimeDir: runtimeDir(name, "primary"),
			DataDir:    dataDir(name, "primary"),
			API:        &blueprint.API{LocalPort: apiPort},
			WinService: &blueprint.WinService{Name: name + "-primary"},
			EventStorage: &blueprint.EventStorage{
				Nats: &blueprint.Nats{
					ClientPort:        clientPort,
					ClusterPort:       clusterPort,
					JetStreamStoreDir: jetstreamStoreDir(name, "primary"),
				},
			},
			Standby: standby,
		},
	}
}

// runtimeDir is one instance's own local runtime directory.
func runtimeDir(machine, role string) string {
	return fmt.Sprintf("C:/ProgramData/opdl/%s/%s", machine, role)
}

// dataDir is one instance's general platform data root. It is separate
// from runtimeDir because the two live on different volumes in a real
// deployment: runtime status is disposable, while platform data is retained.
func dataDir(machine, role string) string {
	return fmt.Sprintf("D:/opdl-data/%s/%s", machine, role)
}

// addr is the address a machine's Event Fabric listener is reached on.
func addr(ip string, port int) string { return fmt.Sprintf("%s:%d", ip, port) }

// site builds a site of machinesCount machines named node-1..node-N with
// sequential ips, declared in reverse name order so a test can tell derived
// ordering from declaration order.
//
// Every machine deploys only a Primary Instance, so the machine count is also the
// instance count. That means machinesCount must be at least three: the platform's
// minimum site is two machines and three instances, and a site of one or two
// single-instance machines is rejected before anything a caller is testing runs.
func site(name string, machineCount int) blueprint.Site {
	machines := make([]blueprint.Machine, 0, machineCount)
	for i := machineCount; i >= 1; i-- {
		machines = append(machines, machine(fmt.Sprintf("node-%d", i), fmt.Sprintf("10.0.1.%d", i), true))
	}
	return blueprint.Site{Name: name, Machines: machines}
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
		Features:    blueprint.Features{Chaos: true},
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
			machine("south-relay", "10.0.2.11", false),
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
	require.True(t, m.Features.Chaos)
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

// TestBuildDerivesEventFabricPeersFromTheSiteOnly checks the fabric spans exactly one
// site: a machine's peers are its site's other machines, never another site's,
// and every machine of a site sees the same membership.
func TestBuildDerivesEventFabricPeersFromTheSiteOnly(t *testing.T) {
	plan, err := resolve.Build(twoSiteProject(), "acme-opdl")
	require.NoError(t, err)

	// The north site's machines each deploy one instance here, so its membership is
	// three peers ordered by machine name, and it includes this machine's own.
	sensor := machineByName(t, plan, "sensor")
	names := make([]string, 0, len(sensor.Peers))
	for _, peer := range sensor.Peers {
		require.Equal(t, "north", peer.Site)
		names = append(names, peer.Machine+"/"+string(peer.Role))
	}
	require.Equal(t, []string{"archive/primary", "gateway/primary", "sensor/primary"}, names)

	// The site has three machines, so all three store the journal and route to
	// each other. Storage order is by machine name: archive, gateway, sensor.
	require.Equal(t, &deployment.Nats{
		JetStreamStoreDir: jetstreamStoreDir("sensor", "primary"),
		ClientAddress:     addr("10.0.1.10", clientPort),
		ClusterAddress:    addr("10.0.1.10", clusterPort),
		Servers:           []string{addr("10.0.1.10", clientPort), addr("10.0.1.12", clientPort), addr("10.0.1.11", clientPort)},
		Routes:            []string{addr("10.0.1.12", clusterPort), addr("10.0.1.11", clusterPort)},
	}, sensor.Instances.Primary.Nats)

	// The south site forms its own fabric and never meets the north machines. Its
	// membership is its own two machines' three instances, and nothing of north's.
	south := machineByName(t, plan, "south-node")
	southNames := make([]string, 0, len(south.Peers))
	for _, peer := range south.Peers {
		require.Equal(t, "south", peer.Site)
		southNames = append(southNames, peer.Machine+"/"+string(peer.Role))
	}
	require.Equal(t, []string{"south-node/primary", "south-relay/primary", "south-relay/standby"}, southNames)
}

// TestBuildDerivesStorageTopologyBySiteSize checks the listener matrix the
// contract promises: how many machines store the journal, which of them bind a
// cluster listener, and what a machine that stores nothing still knows.
//
// The cluster listener is the interesting column. Every site the platform accepts
// has at least three platform instances, so the storage selection is always three
// and there is always a real cluster; what varies is how many machines are
// clients of it. The one-storage-node shape the selection still codes for is
// unreachable through a valid blueprint, which is why it is not a case here.
func TestBuildDerivesStorageTopologyBySiteSize(t *testing.T) {
	tests := []struct {
		siteSize     int
		storage      []string
		wantRoutes   map[string]int
		wantServers  int
		clusterNodes int
	}{
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
				nats := d.Instances.Primary.Nats
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
			machineByName(t, authored, name).Instances,
			machineByName(t, shuffled, name).Instances,
			"machine %s", name)
	}
}

// TestBuildResolvesEmptyListsAsEmptyArrays checks an empty route list is an
// empty JSON array rather than null, so a descriptor reader can tell "resolved
// to nothing" from "not resolved".
func TestBuildResolvesEmptyListsAsEmptyArrays(t *testing.T) {
	// A site of four single-instance machines: the first three by name store the
	// journal, and the fourth routes to nothing. Every valid site has at least
	// three storage instances, so an empty route list now belongs to an instance
	// the selection left out rather than to a site too small to cluster.
	plan, err := resolve.Build(projectOf(site("north", 4)), "acme-opdl")
	require.NoError(t, err)
	client := machineByName(t, plan, "node-4")
	require.NotNil(t, client.Instances.Primary.Nats.Routes)
	require.Empty(t, client.Instances.Primary.Nats.Routes)
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
func TestBuildCarriesAuthoredLock(t *testing.T) {
	t.Run("standby deployed carries lock", func(t *testing.T) {
		p := projectOf(blueprint.Site{
			Name:     "north",
			Machines: []blueprint.Machine{machine("sensor", "10.0.1.10", false), companion()},
		})
		plan, err := resolve.Build(p, "acme-opdl")
		require.NoError(t, err)
		require.Equal(t, &deployment.Lock{WindowsMutex: "Global\\opdl-sensor"}, plan.Machines[0].Lock)
	})

	t.Run("standby disabled yields nil lock", func(t *testing.T) {
		p := projectOf(blueprint.Site{
			Name:     "north",
			Machines: []blueprint.Machine{machine("sensor", "10.0.1.10", true), companion()},
		})
		plan, err := resolve.Build(p, "acme-opdl")
		require.NoError(t, err)
		require.Nil(t, plan.Machines[0].Lock)
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
