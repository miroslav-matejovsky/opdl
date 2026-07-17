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
		{"missing platform", func(d *deployment.Descriptor) { d.Platform = "" }, "platform is required"},
		{"missing project", func(d *deployment.Descriptor) { d.Project = "" }, "project is required"},
		{"missing environment", func(d *deployment.Descriptor) { d.Environment = "" }, "environment is required"},
		{"missing site", func(d *deployment.Descriptor) { d.Site = "" }, "site is required"},
		{"missing machine", func(d *deployment.Descriptor) { d.Machine = "" }, "machine is required"},
		{"missing role", func(d *deployment.Descriptor) { d.Role = "" }, "role is required"},
		{"invalid ip", func(d *deployment.Descriptor) { d.IP = "not-an-ip" }, "not a valid IP address"},
		{"no services", func(d *deployment.Descriptor) { d.Services = nil }, "at least one service is required"},
		{
			"peer from another site",
			func(d *deployment.Descriptor) { d.EventFabric.Peers[0].Site = "south" },
			`event fabric peer "gateway" is in site "south", not this machine's site "north"`,
		},
		{
			"machine lists itself as a peer",
			func(d *deployment.Descriptor) { d.EventFabric.Peers[0].Machine = "sensor" },
			`event fabric peer "sensor" is duplicated or is this machine itself`,
		},
		{
			"duplicate peer",
			func(d *deployment.Descriptor) { d.EventFabric.Peers[1].Machine = "gateway" },
			"is duplicated",
		},
		{
			"peer with empty machine",
			func(d *deployment.Descriptor) { d.EventFabric.Peers[0].Machine = " " },
			"event fabric peer with empty machine",
		},
		{
			"peer with invalid ip",
			func(d *deployment.Descriptor) { d.EventFabric.Peers[0].IP = "nope" },
			`event fabric peer "gateway": ip "nope" is not a valid IP address`,
		},
		{
			"peer reusing this machine's ip",
			func(d *deployment.Descriptor) { d.EventFabric.Peers[0].IP = "10.0.1.10" },
			`ip "10.0.1.10" is already used by another member`,
		},
		{
			"peers sharing an ip",
			func(d *deployment.Descriptor) { d.EventFabric.Peers[1].IP = "10.0.1.11" },
			`ip "10.0.1.11" is already used by another member`,
		},
		{
			"peers out of order",
			func(d *deployment.Descriptor) {
				d.EventFabric.Peers[0], d.EventFabric.Peers[1] = d.EventFabric.Peers[1], d.EventFabric.Peers[0]
			},
			`event fabric peers are not ordered by machine: "gateway" after "historian"`,
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
