package deployment_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/builder/deployment"
)

func validDescriptor() deployment.Descriptor {
	return deployment.Descriptor{
		Platform:    "opdl",
		Project:     "customer-a",
		Environment: "production",
		Site:        "north",
		Machine:     "sensor",
		Role:        "sensor-node",
		IP:          "10.0.1.10",
		Services:    []string{"sensor-services"},
		Features:    deployment.Features{Chaos: true},
		Slots: deployment.Slots{
			Primary: deployment.Slot{Disabled: false},
			Standby: deployment.Slot{Disabled: false},
		},
		EventFabric: deployment.EventFabric{
			Nats: deployment.EventFabricNats{
				ClientAddress:  "10.0.1.10:4222",
				ClusterAddress: "10.0.1.10:6222",
				Routes:         []string{"10.0.1.11:6222", "10.0.1.12:6222"},
				Servers:        []string{"10.0.1.10:4222", "10.0.1.11:4222", "10.0.1.12:4222"},
			},
			Peers: []deployment.EventFabricPeer{
				{Site: "north", Machine: "gateway", IP: "10.0.1.11"},
				{Site: "north", Machine: "historian", IP: "10.0.1.12"},
			},
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
		{
			"missing platform",
			func(d *deployment.Descriptor) { d.Platform = "" },
			"platform is required",
		},
		{
			"missing project",
			func(d *deployment.Descriptor) { d.Project = "" },
			"project is required",
		},
		{
			"missing environment",
			func(d *deployment.Descriptor) { d.Environment = "" },
			"environment is required",
		},
		{
			"missing site",
			func(d *deployment.Descriptor) { d.Site = "" },
			"site is required",
		},
		{
			"missing machine",
			func(d *deployment.Descriptor) { d.Machine = "" },
			"machine is required",
		},
		{
			"missing role",
			func(d *deployment.Descriptor) { d.Role = "" },
			"role is required",
		},
		{
			"missing ip",
			func(d *deployment.Descriptor) { d.IP = "" },
			`ip "" is not a valid IP address`,
		},
		{
			"invalid ip",
			func(d *deployment.Descriptor) { d.IP = "bad-ip" },
			`ip "bad-ip" is not a valid IP address`,
		},
		{
			"missing services",
			func(d *deployment.Descriptor) { d.Services = nil },
			"at least one service is required",
		},
		{
			"duplicate peer machine",
			func(d *deployment.Descriptor) {
				d.EventFabric.Peers = append(d.EventFabric.Peers, d.EventFabric.Peers[0])
			},
			`event fabric peer "gateway" is duplicated or is this machine itself`,
		},
		{
			"duplicate peer ip",
			func(d *deployment.Descriptor) {
				d.EventFabric.Peers[1].IP = d.EventFabric.Peers[0].IP
			},
			`ip "10.0.1.11" is already used by another member`,
		},
		{
			"peers out of order",
			func(d *deployment.Descriptor) {
				d.EventFabric.Peers[0], d.EventFabric.Peers[1] = d.EventFabric.Peers[1], d.EventFabric.Peers[0]
			},
			`event fabric peers are not ordered by machine: "gateway" after "historian"`,
		},
		{
			"disabled primary",
			func(d *deployment.Descriptor) { d.Slots.Primary.Disabled = true },
			"slots.primary.disabled: a machine must deploy a primary process",
		},
		{
			"missing client_address",
			func(d *deployment.Descriptor) { d.EventFabric.Nats.ClientAddress = "" },
			"event_fabric.nats.client_address is required",
		},
		{
			"invalid client_address",
			func(d *deployment.Descriptor) { d.EventFabric.Nats.ClientAddress = "no-port" },
			"event_fabric.nats.client_address:",
		},
		{
			"missing cluster_address",
			func(d *deployment.Descriptor) { d.EventFabric.Nats.ClusterAddress = "" },
			"event_fabric.nats.cluster_address is required",
		},
		{
			"missing servers",
			func(d *deployment.Descriptor) { d.EventFabric.Nats.Servers = nil },
			"event_fabric.nats.servers: at least one server is required",
		},
		{
			"duplicate server",
			func(d *deployment.Descriptor) {
				d.EventFabric.Nats.Servers = append(d.EventFabric.Nats.Servers, d.EventFabric.Nats.Servers[1])
			},
			`event_fabric.nats.servers: "10.0.1.11:4222" is listed twice`,
		},
		{
			"duplicate route",
			func(d *deployment.Descriptor) {
				d.EventFabric.Nats.Routes = append(d.EventFabric.Nats.Routes, d.EventFabric.Nats.Routes[0])
			},
			`event_fabric.nats.routes: "10.0.1.11:6222" is listed twice`,
		},
		{
			"route points at this machine",
			func(d *deployment.Descriptor) {
				d.EventFabric.Nats.Routes[0] = d.EventFabric.Nats.ClusterAddress
			},
			`event_fabric.nats.routes: "10.0.1.10:6222" is this machine itself`,
		},
		{
			// A storage machine that does not list itself first would send its
			// own traffic to a peer while its local server is up.
			"storage machine does not list itself first",
			func(d *deployment.Descriptor) {
				d.EventFabric.Nats.Servers[0], d.EventFabric.Nats.Servers[1] =
					d.EventFabric.Nats.Servers[1], d.EventFabric.Nats.Servers[0]
			},
			"a storage machine must list its own client address",
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

// TestDescriptorValidateAcceptsOneMemberFabric checks a single-machine site is a
// valid deployment: it forms a fabric with itself and no peers. It stores the
// journal alone, so it has no peer server to route to.
func TestDescriptorValidateAcceptsOneMemberFabric(t *testing.T) {
	d := validDescriptor()
	d.EventFabric.Peers = nil
	d.EventFabric.Nats.Routes = []string{}
	d.EventFabric.Nats.Servers = []string{"10.0.1.10:4222"}
	require.NoError(t, d.Validate())
}

// TestDescriptorValidateRejectsRoutesWithoutACluster checks a machine cannot
// carry routes its site's storage selection does not justify.
//
// Both shapes are rejected for the same reason: a cluster listener is bound
// because routes exist, so a route resolved where no peer server runs would open
// a port nothing can connect to.
func TestDescriptorValidateRejectsRoutesWithoutACluster(t *testing.T) {
	t.Run("non-storage machine", func(t *testing.T) {
		d := validDescriptor()
		// A fourth machine sorts after the first three, so it stores nothing.
		d.Machine = "zulu"
		d.IP = "10.0.1.20"
		d.EventFabric.Nats.ClientAddress = "10.0.1.20:4222"
		d.EventFabric.Nats.ClusterAddress = "10.0.1.20:6222"
		d.EventFabric.Peers = append(d.EventFabric.Peers,
			deployment.EventFabricPeer{Site: "north", Machine: "sensor", IP: "10.0.1.10"})
		require.ErrorContains(t, d.Validate(),
			"a machine that does not store the journal has no cluster to route to")
	})

	t.Run("site with one storage machine", func(t *testing.T) {
		d := validDescriptor()
		d.EventFabric.Peers = nil
		d.EventFabric.Nats.Servers = []string{"10.0.1.10:4222"}
		require.ErrorContains(t, d.Validate(),
			"a site with 1 storage machine(s) has no routes")
	})
}

// TestDescriptorValidateAcceptsNonStorageMachine checks a machine that stores
// nothing is still a valid deployment: it knows every storage server, lists none
// of its own addresses among them, and binds nothing.
func TestDescriptorValidateAcceptsNonStorageMachine(t *testing.T) {
	d := validDescriptor()
	d.Machine = "zulu"
	d.IP = "10.0.1.20"
	d.EventFabric.Nats.ClientAddress = "10.0.1.20:4222"
	d.EventFabric.Nats.ClusterAddress = "10.0.1.20:6222"
	d.EventFabric.Nats.Routes = []string{}
	d.EventFabric.Peers = append(d.EventFabric.Peers,
		deployment.EventFabricPeer{Site: "north", Machine: "sensor", IP: "10.0.1.10"})
	require.NoError(t, d.Validate())
}
