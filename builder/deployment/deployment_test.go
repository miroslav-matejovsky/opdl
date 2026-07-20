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
			Primary: deployment.Slot{
				EventFabric: deployment.SlotEventFabric{
					Nats: deployment.EventFabricNats{
						ClientAddress:  "10.0.1.10:4222",
						ClusterAddress: "10.0.1.10:6222",
						MonitorAddress: "127.0.0.1:8222",
						Servers:        []string{"10.0.1.10:4222", "10.0.1.11:4222", "10.0.1.12:4222"},
					},
				},
			},
			Standby: &deployment.Slot{
				EventFabric: deployment.SlotEventFabric{
					Nats: deployment.EventFabricNats{
						ClientAddress:  "10.0.1.10:4223",
						ClusterAddress: "10.0.1.10:6223",
						MonitorAddress: "127.0.0.1:8223",
						Servers:        []string{"10.0.1.10:4223", "10.0.1.11:4223", "10.0.1.12:4223"},
					},
				},
			},
		},
		EventFabric: deployment.EventFabric{
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
			"missing primary nats client_address",
			func(d *deployment.Descriptor) { d.Slots.Primary.EventFabric.Nats.ClientAddress = "" },
			"slots.primary.event_fabric.nats.client_address is required",
		},
		{
			"missing primary nats cluster_address",
			func(d *deployment.Descriptor) { d.Slots.Primary.EventFabric.Nats.ClusterAddress = "" },
			"slots.primary.event_fabric.nats.cluster_address is required",
		},
		{
			"missing primary nats monitor_address",
			func(d *deployment.Descriptor) { d.Slots.Primary.EventFabric.Nats.MonitorAddress = "" },
			"slots.primary.event_fabric.nats.monitor_address is required",
		},
		{
			"missing primary nats servers",
			func(d *deployment.Descriptor) { d.Slots.Primary.EventFabric.Nats.Servers = nil },
			"slots.primary.event_fabric.nats.servers: at least one server is required",
		},
		{
			"missing standby nats client_address",
			func(d *deployment.Descriptor) { d.Slots.Standby.EventFabric.Nats.ClientAddress = "" },
			"slots.standby.event_fabric.nats.client_address is required",
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
// valid deployment: it forms a fabric with itself and no peers.
func TestDescriptorValidateAcceptsOneMemberFabric(t *testing.T) {
	d := validDescriptor()
	d.EventFabric.Peers = nil
	require.NoError(t, d.Validate())
}
