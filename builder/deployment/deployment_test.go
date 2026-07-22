package deployment_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/builder/deployment"
)

// The fixture is a three-machine site where this machine deploys both instances
// and its peers deploy one each. That is the shape the rules are about: four
// storage servers on three machines, two of them sharing a host.
const (
	machineIP   = "10.0.1.10"
	gatewayIP   = "10.0.1.11"
	historianIP = "10.0.1.12"

	// The api addresses are on loopback; every Event Fabric address is on the
	// machine ip. That split is what the descriptor is checked against.
	primaryAPI        = "127.0.0.1:8080"
	primaryRuntime    = "C:/ProgramData/opdl/sensor/primary"
	primaryDataDir    = "D:/opdl-journal/sensor/primary"
	primaryClient     = "10.0.1.10:4222"
	primaryCluster    = "10.0.1.10:6222"
	standbyAPI        = "127.0.0.1:8081"
	standbyRuntimeDir = "C:/ProgramData/opdl/sensor/standby"
	standbyDataDir    = "D:/opdl-journal/sensor/standby"
	standbyClient     = "10.0.1.10:4322"
	standbyCluster    = "10.0.1.10:6322"

	gatewayClient    = "10.0.1.11:4222"
	gatewayCluster   = "10.0.1.11:6222"
	historianClient  = "10.0.1.12:4222"
	historianCluster = "10.0.1.12:6222"
)

func peer(machine string, role deployment.PlatformInstanceRole, ip, client, cluster string) deployment.Peer {
	return deployment.Peer{
		Site: "north", Machine: machine, Role: role, IP: ip,
		Nats: deployment.PeerNats{ClientAddress: client, ClusterAddress: cluster},
	}
}

func validDescriptor() deployment.Descriptor {
	return deployment.Descriptor{
		Platform:       "opdl",
		Project:        "customer-a",
		Environment:    "production",
		Site:           "north",
		Machine:        "sensor",
		MachineProfile: "sensor-node",
		IP:             machineIP,
		Services:       []string{"sensor-services"},
		Features:       deployment.Features{Chaos: true},
		Instances: deployment.Instances{
			Primary: deployment.Instance{
				Disabled:   false,
				Service:    &deployment.WinService{Name: "sensor-primary", DisplayName: "sensor primary"},
				RuntimeDir: primaryRuntime,
				DataDir:    primaryDataDir,
				APIAddress: primaryAPI,
				Nats: &deployment.Nats{
					JetStreamStoreDir: primaryDataDir + "/eventfabric/nats",
					ClientAddress:     primaryClient,
					ClusterAddress:    primaryCluster,
					Routes:            []string{gatewayCluster, standbyCluster},
					Servers:           []string{primaryClient, gatewayClient, standbyClient},
				},
			},
			Standby: deployment.Instance{
				Disabled:   false,
				Service:    &deployment.WinService{Name: "sensor-standby", DisplayName: "sensor standby"},
				RuntimeDir: standbyRuntimeDir,
				DataDir:    standbyDataDir,
				APIAddress: standbyAPI,
				Nats: &deployment.Nats{
					JetStreamStoreDir: standbyDataDir + "/eventfabric/nats",
					ClientAddress:     standbyClient,
					ClusterAddress:    standbyCluster,
					Routes:            []string{gatewayCluster, primaryCluster},
					Servers:           []string{standbyClient, gatewayClient, primaryClient},
				},
			},
		},
		Lock: &deployment.Lock{WindowsMutex: "Global\\opdl-customer-a-north-sensor"},
		// The platform's minimum site: two machines and three platform instances.
		// Three is also exactly the storage selection, so every instance here runs
		// a server and routes to the other two. Fixtures that need a non-storage
		// instance add a fourth.
		Peers: []deployment.Peer{
			peer("gateway", deployment.RolePrimary, gatewayIP, gatewayClient, gatewayCluster),
			peer("sensor", deployment.RolePrimary, machineIP, primaryClient, primaryCluster),
			peer("sensor", deployment.RoleStandby, machineIP, standbyClient, standbyCluster),
		},
	}
}

func TestDescriptorValidateOK(t *testing.T) {
	require.NoError(t, validDescriptor().Validate())
}

func TestDescriptorValidateFailures(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*deployment.Descriptor)
		errText string
	}{
		{"missing platform", func(d *deployment.Descriptor) { d.Platform = "" }, "platform is required"},
		{"missing project", func(d *deployment.Descriptor) { d.Project = "" }, "project is required"},
		{"missing environment", func(d *deployment.Descriptor) { d.Environment = "" }, "environment is required"},
		{"missing site", func(d *deployment.Descriptor) { d.Site = "" }, "site is required"},
		{"missing machine", func(d *deployment.Descriptor) { d.Machine = "" }, "machine is required"},
		{"missing machine profile", func(d *deployment.Descriptor) { d.MachineProfile = "" }, "machine profile is required"},
		{"missing ip", func(d *deployment.Descriptor) { d.IP = "" }, `ip "" is not a valid IP address`},
		{"invalid ip", func(d *deployment.Descriptor) { d.IP = "bad-ip" }, `ip "bad-ip" is not a valid IP address`},
		{"missing services", func(d *deployment.Descriptor) { d.Services = nil }, "at least one service is required"},
		{"missing lock when standby deployed", func(d *deployment.Descriptor) { d.Lock = nil }, "lock is required when instances.standby.disabled is false"},
		{"missing lock windows_mutex", func(d *deployment.Descriptor) { d.Lock = &deployment.Lock{WindowsMutex: ""} }, "lock.windows_mutex is required"},
		{"lock present when standby disabled", func(d *deployment.Descriptor) {
			d.Instances.Standby.Disabled = true
			d.Lock = &deployment.Lock{WindowsMutex: "Global\\opdl-sensor"}
		}, "lock is set but instances.standby.disabled is true; omit lock when no standby is deployed"},
		{
			"disabled primary",
			func(d *deployment.Descriptor) { d.Instances.Primary.Disabled = true },
			"a machine must deploy a Primary Instance",
		},

		// Services.
		{
			"missing primary service",
			func(d *deployment.Descriptor) { d.Instances.Primary.Service = nil },
			"instances.primary.service is required",
		},
		{
			"missing standby service while deployed",
			func(d *deployment.Descriptor) { d.Instances.Standby.Service = nil },
			"instances.standby.service is required",
		},
		{
			"instances share a service name",
			func(d *deployment.Descriptor) {
				d.Instances.Standby.Service.Name = d.Instances.Primary.Service.Name
			},
			"share service name",
		},

		// Endpoints. The two instances run together on one host, so anything
		// resolved onto one address twice is a listener that cannot bind.
		{
			"missing api address",
			func(d *deployment.Descriptor) { d.Instances.Primary.APIAddress = "" },
			"instances.primary.api_address is required",
		},
		{
			"invalid api address",
			func(d *deployment.Descriptor) { d.Instances.Primary.APIAddress = "no-port" },
			"instances.primary.api_address:",
		},
		// The platform API is machine-local. A descriptor resolving it onto the
		// machine's ip would expose every deployment's API to the network, which is
		// exactly what authoring it as api.local_port exists to prevent.
		{
			"api address off loopback",
			func(d *deployment.Descriptor) { d.Instances.Primary.APIAddress = "10.0.1.10:8080" },
			"is not on the loopback interface",
		},
		{
			"api address on a hostname rather than a loopback ip",
			func(d *deployment.Descriptor) { d.Instances.Primary.APIAddress = "localhost:8080" },
			"is not on the loopback interface",
		},
		{
			"missing runtime dir",
			func(d *deployment.Descriptor) { d.Instances.Primary.RuntimeDir = "" },
			"instances.primary.runtime_dir is required",
		},
		{
			"instances share a runtime dir",
			func(d *deployment.Descriptor) {
				d.Instances.Standby.RuntimeDir = d.Instances.Primary.RuntimeDir
			},
			"cannot share a runtime directory",
		},
		{
			"missing nats",
			func(d *deployment.Descriptor) { d.Instances.Primary.Nats = nil },
			"instances.primary.nats is required",
		},
		{
			"instances share an api address",
			func(d *deployment.Descriptor) {
				d.Instances.Standby.APIAddress = d.Instances.Primary.APIAddress
			},
			"cannot share a listener",
		},
		{
			"instances share a nats client address",
			func(d *deployment.Descriptor) {
				d.Instances.Standby.Nats.ClientAddress = d.Instances.Primary.Nats.ClientAddress
			},
			"cannot share a listener",
		},

		// A standby that is not deployed carries nothing it would have bound.
		{
			"standby service while disabled",
			func(d *deployment.Descriptor) {
				d.Instances.Standby.Disabled = true
				d.Lock = nil
				d.Instances.Standby.APIAddress = ""
				d.Instances.Standby.Nats = nil
			},
			"instances.standby.service is set but the standby is disabled",
		},
		{
			"standby api address while disabled",
			func(d *deployment.Descriptor) {
				d.Instances.Standby.Disabled = true
				d.Lock = nil
				d.Instances.Standby.Service = nil
				d.Instances.Standby.Nats = nil
			},
			"instances.standby.api_address is set but the standby is disabled",
		},
		{
			"standby runtime dir while disabled",
			func(d *deployment.Descriptor) {
				d.Instances.Standby.Disabled = true
				d.Lock = nil
				d.Instances.Standby.Service = nil
				d.Instances.Standby.APIAddress = ""
				d.Instances.Standby.Nats = nil
			},
			"instances.standby.runtime_dir is set but the standby is disabled",
		},

		// Peers are instances, and every listener in the site is distinct.
		{
			"duplicate peer",
			func(d *deployment.Descriptor) { d.Peers = append(d.Peers, d.Peers[0]) },
			`peer gateway/primary is listed twice`,
		},
		{
			"peer in another site",
			func(d *deployment.Descriptor) { d.Peers[0].Site = "south" },
			`is in site "south", not this machine's site "north"`,
		},
		{
			"peer with an unknown role",
			func(d *deployment.Descriptor) { d.Peers[0].Role = "spare" },
			`has role "spare", want "primary" or "standby"`,
		},
		{
			"peers out of order",
			func(d *deployment.Descriptor) { d.Peers[0], d.Peers[1] = d.Peers[1], d.Peers[0] },
			"peers are not ordered by machine then role",
		},
		{
			"two peers claim one address",
			func(d *deployment.Descriptor) { d.Peers[1].Nats.ClientAddress = d.Peers[0].Nats.ClientAddress },
			"is already used by",
		},
		{
			"machine is missing from its own site membership",
			// Drops the last peer, which is this machine's own Standby Instance.
			func(d *deployment.Descriptor) { d.Peers = d.Peers[:len(d.Peers)-1] },
			"peers do not include this machine's own standby instance",
		},

		// Resolved NATS topology.
		{
			"missing servers",
			func(d *deployment.Descriptor) { d.Instances.Primary.Nats.Servers = nil },
			"instances.primary.nats.servers: at least one server is required",
		},
		{
			"duplicate server",
			func(d *deployment.Descriptor) {
				nats := d.Instances.Primary.Nats
				nats.Servers = append(nats.Servers, nats.Servers[1])
			},
			`instances.primary.nats.servers: "` + gatewayClient + `" is listed twice`,
		},
		{
			"duplicate route",
			func(d *deployment.Descriptor) {
				nats := d.Instances.Primary.Nats
				nats.Routes = append(nats.Routes, nats.Routes[0])
			},
			`instances.primary.nats.routes: "` + gatewayCluster + `" is listed twice`,
		},
		{
			"route points at this instance",
			func(d *deployment.Descriptor) {
				d.Instances.Primary.Nats.Routes[0] = d.Instances.Primary.Nats.ClusterAddress
			},
			`instances.primary.nats.routes: "` + primaryCluster + `" is this instance itself`,
		},
		{
			// An instance on a storage machine that does not list itself first
			// would send its own traffic to a peer while its local server is up.
			"storage instance does not list itself first",
			func(d *deployment.Descriptor) {
				nats := d.Instances.Primary.Nats
				nats.Servers[0], nats.Servers[1] = nats.Servers[1], nats.Servers[0]
			},
			"must list its own client address",
		},
		{
			// Every other storage server contributes one route and one server, so
			// a dropped server breaks the relation whatever the site's shape.
			"a storage server is missing a route",
			func(d *deployment.Descriptor) {
				nats := d.Instances.Primary.Nats
				nats.Routes = nats.Routes[:len(nats.Routes)-1]
			},
			"an instance routes to every storage server but its own",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := validDescriptor()
			tc.mutate(&d)
			require.ErrorContains(t, d.Validate(), tc.errText)
		})
	}
}

// TestDescriptorValidateAcceptsOneInstanceMachine checks a machine that deploys
// no standby is a valid deployment, and that it then carries nothing the absent
// instance would have bound.
func TestDescriptorValidateAcceptsOneInstanceMachine(t *testing.T) {
	d := validDescriptor()
	d.Instances.Standby = deployment.Instance{Disabled: true}
	d.Lock = nil
	d.Peers = d.Peers[:3]
	nats := d.Instances.Primary.Nats
	nats.Routes = []string{gatewayCluster, historianCluster}
	nats.Servers = []string{primaryClient, gatewayClient, historianClient}
	require.NoError(t, d.Validate())
}

// TestDescriptorValidateAcceptsOneMemberSite checks a single-machine,
// single-instance site is a valid deployment: it stores the journal alone, so it
// has one server and no peer to route to.
func TestDescriptorValidateAcceptsOneMemberSite(t *testing.T) {
	d := validDescriptor()
	d.Instances.Standby = deployment.Instance{Disabled: true}
	d.Lock = nil
	d.Peers = []deployment.Peer{
		peer("sensor", deployment.RolePrimary, machineIP, primaryClient, primaryCluster),
	}
	nats := d.Instances.Primary.Nats
	nats.Routes = []string{}
	nats.Servers = []string{primaryClient}
	require.NoError(t, d.Validate())
}

// TestDescriptorValidateRejectsRoutesOnANonStorageMachine checks an instance
// cannot carry routes its site's storage selection does not justify. A cluster
// listener is bound because routes exist, so a route where no peer server runs
// would open a port nothing can connect to.
func TestDescriptorValidateRejectsRoutesOnANonStorageMachine(t *testing.T) {
	d := nonStorageDescriptor()
	require.ErrorContains(t, d.Validate(),
		"an instance that does not store the journal has no cluster to route to")
}

// TestDescriptorValidateAcceptsNonStorageMachine checks a machine that stores
// nothing is still a valid deployment: it knows every storage server, lists none
// of its own addresses among them, and binds no cluster listener.
func TestDescriptorValidateAcceptsNonStorageMachine(t *testing.T) {
	d := nonStorageDescriptor()
	for _, instance := range []*deployment.Instance{&d.Instances.Primary, &d.Instances.Standby} {
		instance.Nats.Routes = []string{}
		instance.Nats.Servers = []string{gatewayClient, historianClient}
	}
	require.NoError(t, d.Validate())
}

// nonStorageDescriptor is a machine that sorts after the site's first three, so
// the storage selection excludes it. Its own instances keep their addresses,
// which it never binds.
func nonStorageDescriptor() deployment.Descriptor {
	const (
		zuluIP = "10.0.1.20"
		// The api addresses stay on loopback: they are this machine's own, and a
		// machine's api is machine-local whether or not it stores the journal.
		zuluAPI            = "127.0.0.1:8080"
		zuluClient         = "10.0.1.20:4222"
		zuluCluster        = "10.0.1.20:6222"
		zuluStandbyAPI     = "127.0.0.1:8081"
		zuluStandbyClient  = "10.0.1.20:4322"
		zuluStandbyCluster = "10.0.1.20:6322"
	)
	d := validDescriptor()
	d.Machine = "zulu"
	d.IP = zuluIP
	d.Instances.Primary.APIAddress = zuluAPI
	d.Instances.Primary.Nats.ClientAddress = zuluClient
	d.Instances.Primary.Nats.ClusterAddress = zuluCluster
	d.Instances.Standby.APIAddress = zuluStandbyAPI
	d.Instances.Standby.Nats.ClientAddress = zuluStandbyClient
	d.Instances.Standby.Nats.ClusterAddress = zuluStandbyCluster
	d.Peers = []deployment.Peer{
		peer("gateway", deployment.RolePrimary, gatewayIP, gatewayClient, gatewayCluster),
		peer("historian", deployment.RolePrimary, historianIP, historianClient, historianCluster),
		peer("sensor", deployment.RolePrimary, machineIP, primaryClient, primaryCluster),
		peer("zulu", deployment.RolePrimary, zuluIP, zuluClient, zuluCluster),
		peer("zulu", deployment.RoleStandby, zuluIP, zuluStandbyClient, zuluStandbyCluster),
	}
	return d
}
