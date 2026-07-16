package olric_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	fabricolric "github.com/miroslav-matejovsky/opdl/platform/internal/fabric/olric"
)

func descriptor() deployment.Descriptor {
	return deployment.Descriptor{
		Site:    "north",
		Machine: "node-a",
		IP:      "10.0.1.10",
		Fabric: deployment.Fabric{
			Peers: []deployment.FabricPeer{
				{Site: "north", Machine: "node-b", IP: "10.0.1.11"},
				{Site: "north", Machine: "node-c", IP: "10.0.1.12"},
			},
		},
	}
}

// TestDefaultConfigDerivesEverythingFromTheDescriptor pins the production
// bootstrap: a machine's own addresses and its seeds come from the topology it
// was built with, on fixed ports, and nothing else is needed to join a site.
func TestDefaultConfigDerivesEverythingFromTheDescriptor(t *testing.T) {
	cfg, err := fabricolric.DefaultConfig(descriptor())
	require.NoError(t, err)
	require.Equal(t, fabricolric.Config{
		ClientAddress:     "10.0.1.10:3320",
		MemberlistAddress: "10.0.1.10:3322",
		Join:              []string{"10.0.1.11:3322", "10.0.1.12:3322"},
		StartTimeout:      fabricolric.DefaultStartTimeout,
		ShutdownGrace:     10 * time.Second,
	}, cfg)
	require.NoError(t, cfg.Validate())
}

// TestDefaultConfigForOneMemberSiteSeedsNobody checks a standalone machine
// starts a fabric rather than waiting to join one.
func TestDefaultConfigForOneMemberSiteSeedsNobody(t *testing.T) {
	cfg, err := fabricolric.DefaultConfig(deployment.Descriptor{IP: "127.0.0.1"})
	require.NoError(t, err)
	require.Empty(t, cfg.Join)
	require.NoError(t, cfg.Validate())
}

// TestDefaultStartTimeoutOutlastsAJoinAttempt guards a real failure mode: a
// member whose peers are not up yet pays a memberlist join timeout of about ten
// seconds before starting alone. A tighter default would make the machine that
// boots first fail.
func TestDefaultStartTimeoutOutlastsAJoinAttempt(t *testing.T) {
	require.Greater(t, fabricolric.DefaultStartTimeout, 15*time.Second)
}

func TestDefaultConfigRejectsInvalidTopologyAddresses(t *testing.T) {
	_, err := fabricolric.DefaultConfig(deployment.Descriptor{IP: "not-an-ip"})
	require.ErrorContains(t, err, "client address")

	bad := descriptor()
	bad.Fabric.Peers[1].IP = "nope"
	_, err = fabricolric.DefaultConfig(bad)
	require.ErrorContains(t, err, `peer "node-c"`)
}

func TestConfigValidate(t *testing.T) {
	valid := func() fabricolric.Config {
		cfg, err := fabricolric.DefaultConfig(descriptor())
		require.NoError(t, err)
		return cfg
	}

	tests := []struct {
		name    string
		mutate  func(*fabricolric.Config)
		errText string
	}{
		{"missing client address", func(c *fabricolric.Config) { c.ClientAddress = "" }, "client address is required"},
		{"missing memberlist address", func(c *fabricolric.Config) { c.MemberlistAddress = " " }, "memberlist address is required"},
		{"client address without port", func(c *fabricolric.Config) { c.ClientAddress = "10.0.1.10" }, "must be host:port"},
		{"client address without host", func(c *fabricolric.Config) { c.ClientAddress = ":3320" }, "has no host"},
		{"client port not a number", func(c *fabricolric.Config) { c.ClientAddress = "10.0.1.10:http" }, "port is not a number"},
		{"client port out of range", func(c *fabricolric.Config) { c.ClientAddress = "10.0.1.10:70000" }, "out of range"},
		{"join address invalid", func(c *fabricolric.Config) { c.Join = []string{"nope"} }, "join address"},
		{
			"client and memberlist share an address",
			func(c *fabricolric.Config) { c.MemberlistAddress = c.ClientAddress },
			"client and memberlist addresses are both",
		},
		{
			"machine seeds itself",
			func(c *fabricolric.Config) { c.Join = append(c.Join, c.MemberlistAddress) },
			"is this member itself",
		},
		{
			"duplicate join address",
			func(c *fabricolric.Config) { c.Join = append(c.Join, c.Join[0]) },
			"is listed twice",
		},
		{"zero start timeout", func(c *fabricolric.Config) { c.StartTimeout = 0 }, "start timeout must be positive"},
		{"negative start timeout", func(c *fabricolric.Config) { c.StartTimeout = -time.Second }, "start timeout must be positive"},
		{"zero shutdown grace", func(c *fabricolric.Config) { c.ShutdownGrace = 0 }, "shutdown grace must be positive"},
		{"negative shutdown grace", func(c *fabricolric.Config) { c.ShutdownGrace = -time.Second }, "shutdown grace must be positive"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid()
			test.mutate(&cfg)
			require.ErrorContains(t, cfg.Validate(), test.errText)
		})
	}
}
