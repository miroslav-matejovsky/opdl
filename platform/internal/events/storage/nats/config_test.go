package nats

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/config"
)

func descriptorFor(machine, ip string, nats config.Nats, peers ...config.Peer) config.Descriptor {
	if nats.JetStreamStoreDir == "" {
		nats.JetStreamStoreDir = "/var/lib/opdl/" + machine + "/primary/eventfabric/nats"
	}
	own := config.Peer{
		Site: "north", Machine: machine, Role: config.RolePrimary, IP: ip,
		Nats: config.PeerNats{ClientAddress: nats.ClientAddress, ClusterAddress: nats.ClusterAddress},
	}
	return config.Descriptor{
		Project: "customer-a", Environment: "production", Site: "north",
		Machine: machine, IP: ip,
		Instances: config.Instances{
			Primary: config.Instance{Disabled: false, DataDir: "/var/lib/opdl/" + machine + "/primary", Nats: &nats},
			Standby: config.Instance{Disabled: true},
		},
		Peers: append([]config.Peer{own}, peers...),
	}
}

func descriptorWithStandby(machine, ip string, primary, standby config.Nats, peers ...config.Peer) config.Descriptor {
	if standby.JetStreamStoreDir == "" {
		standby.JetStreamStoreDir = "/var/lib/opdl/" + machine + "/standby/eventfabric/nats"
	}
	d := descriptorFor(machine, ip, primary, peers...)
	d.Instances.Standby = config.Instance{
		Disabled: false,
		DataDir:  "/var/lib/opdl/" + machine + "/standby",
		Nats:     &standby,
	}
	d.Peers = append(d.Peers, config.Peer{
		Site: "north", Machine: machine, Role: config.RoleStandby, IP: ip,
		Nats: config.PeerNats{ClientAddress: standby.ClientAddress, ClusterAddress: standby.ClusterAddress},
	})
	return d
}

func peer(machine, ip string) config.Peer {
	return config.Peer{Site: "north", Machine: machine, Role: config.RolePrimary, IP: ip}
}

func peerInstance(machine, ip string, role config.PlatformInstanceRole, client, cluster string) config.Peer {
	return config.Peer{
		Site: "north", Machine: machine, Role: role, IP: ip,
		Nats: config.PeerNats{ClientAddress: client, ClusterAddress: cluster},
	}
}

func TestStorageNodesSelectsBySortedName(t *testing.T) {
	tests := []struct {
		name      string
		instances []string
		want      []string
	}{
		{name: "empty", instances: nil, want: nil},
		{name: "one instance hosts storage", instances: []string{"node-a-primary"}, want: []string{"node-a-primary"}},
		{name: "two instances use one storage node", instances: []string{"node-b-primary", "node-a-primary"}, want: []string{"node-a-primary"}},
		{name: "three instances all host storage, sorted", instances: []string{"node-c-primary", "node-a-primary", "node-b-primary"}, want: []string{"node-a-primary", "node-b-primary", "node-c-primary"}},
		{name: "larger site uses the first three by name", instances: []string{"node-d-primary", "node-a-primary", "node-c-primary", "node-b-primary"}, want: []string{"node-a-primary", "node-b-primary", "node-c-primary"}},
		{name: "duplicates are compacted", instances: []string{"node-a-primary", "node-a-primary", "node-b-primary"}, want: []string{"node-a-primary"}},
		{
			name:      "two machines with standbys make a three-member cluster",
			instances: []string{"node-a-primary", "node-a-standby", "node-b-primary", "node-b-standby"},
			want:      []string{"node-a-primary", "node-a-standby", "node-b-primary"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, StorageNodes(test.instances))
		})
	}
}

func TestReplicasMatchStorageNodeCount(t *testing.T) {
	require.Equal(t, 1, Replicas(1))
	require.Equal(t, 1, Replicas(2))
	require.Equal(t, 3, Replicas(3))
	require.Equal(t, 3, Replicas(9))
}

func TestDefaultConfigForASingleNodeSiteHostsStorageAlone(t *testing.T) {
	cfg, err := DefaultConfig(descriptorFor("node-a", "10.0.1.10", config.Nats{
		ClientAddress:  "10.0.1.10:4222",
		ClusterAddress: "10.0.1.10:6222",
		Routes:         []string{},
		Servers:        []string{"10.0.1.10:4222"},
	}), config.RolePrimary)
	require.NoError(t, err)

	require.True(t, cfg.HostsStorage, "the only machine hosts storage")
	require.Equal(t, 1, cfg.Replicas)
	require.Empty(t, cfg.Routes, "a single-node site has no other storage node to cluster with")
	require.Equal(t, "10.0.1.10:4222", cfg.ClientAddress)
	require.Equal(t, "10.0.1.10:6222", cfg.ClusterAddress)
	require.Equal(t, "node-a-primary", cfg.ServerName)
	require.Equal(t, "node-a-primary", cfg.ClientName)
	require.NotEmpty(t, cfg.ClusterName)
	require.Equal(t, []string{"10.0.1.10:4222"}, cfg.Servers, "a storage node reaches the journal on its own server")
}

func TestDefaultConfigReadsTheRunningInstancesOwnTopology(t *testing.T) {
	descriptor := descriptorWithStandby("node-a", "10.0.1.10",
		config.Nats{
			ClientAddress:  "10.0.1.10:4222",
			ClusterAddress: "10.0.1.10:6222",
			Routes:         []string{"10.0.1.10:6322", "10.0.1.11:6222"},
			Servers:        []string{"10.0.1.10:4222", "10.0.1.10:4322", "10.0.1.11:4222"},
		},
		config.Nats{
			ClientAddress:  "10.0.1.10:4322",
			ClusterAddress: "10.0.1.10:6322",
			Routes:         []string{"10.0.1.10:6222", "10.0.1.11:6222"},
			Servers:        []string{"10.0.1.10:4322", "10.0.1.10:4222", "10.0.1.11:4222"},
		},
		peerInstance("node-b", "10.0.1.11", config.RolePrimary, "10.0.1.11:4222", "10.0.1.11:6222"),
		peerInstance("node-b", "10.0.1.11", config.RoleStandby, "10.0.1.11:4322", "10.0.1.11:6322"))

	primary, err := DefaultConfig(descriptor, config.RolePrimary)
	require.NoError(t, err)
	standby, err := DefaultConfig(descriptor, config.RoleStandby)
	require.NoError(t, err)

	require.True(t, primary.HostsStorage, "the machine stores the journal")
	require.True(t, standby.HostsStorage, "a Passive instance is a full cluster member, not a lesser one")

	require.Equal(t, "10.0.1.10:4222", primary.ClientAddress)
	require.Equal(t, "10.0.1.10:4322", standby.ClientAddress)
	require.Equal(t, "10.0.1.10:6222", primary.ClusterAddress)
	require.Equal(t, "10.0.1.10:6322", standby.ClusterAddress)

	require.Equal(t, []string{"10.0.1.10:6322", "10.0.1.11:6222"}, primary.Routes)
	require.Equal(t, []string{"10.0.1.10:6222", "10.0.1.11:6222"}, standby.Routes)

	require.Equal(t, "node-a-primary", primary.ServerName)
	require.Equal(t, "node-a-standby", standby.ServerName)
	require.NotEqual(t, primary.JetStreamStoreDir, standby.JetStreamStoreDir)
	require.Equal(t, "/var/lib/opdl/node-a/primary/eventfabric/nats", primary.JetStreamStoreDir)
	require.Equal(t, "/var/lib/opdl/node-a/standby/eventfabric/nats", standby.JetStreamStoreDir)

	require.Equal(t, 3, primary.Replicas)
	require.Equal(t, 3, standby.Replicas)

	require.Equal(t, "10.0.1.10:4222", primary.Servers[0])
	require.Equal(t, "10.0.1.10:4322", standby.Servers[0])
}

func TestDefaultConfigRejectsAnInstanceTheMachineDoesNotDeploy(t *testing.T) {
	_, err := DefaultConfig(descriptorFor("node-a", "10.0.1.10", config.Nats{
		ClientAddress:  "10.0.1.10:4222",
		ClusterAddress: "10.0.1.10:6222",
		Routes:         []string{},
		Servers:        []string{"10.0.1.10:4222"},
	}), config.RoleStandby)
	require.ErrorContains(t, err, "does not deploy the standby instance")
}

func TestDefaultConfigForATwoMachineSiteRunsOneServer(t *testing.T) {
	storage, err := DefaultConfig(descriptorFor("node-a", "10.0.1.10", config.Nats{
		ClientAddress:  "10.0.1.10:4222",
		ClusterAddress: "10.0.1.10:6222",
		Routes:         []string{},
		Servers:        []string{"10.0.1.10:4222"},
	}, peer("node-b", "10.0.1.11")), config.RolePrimary)
	require.NoError(t, err)
	require.True(t, storage.HostsStorage, "node-a sorts first")
	require.Empty(t, storage.Routes, "the site's only server has nobody to cluster with")
	require.Equal(t, []string{"10.0.1.10:4222"}, storage.Servers)

	client, err := DefaultConfig(descriptorFor("node-b", "10.0.1.11", config.Nats{
		ClientAddress:  "10.0.1.11:4222",
		ClusterAddress: "10.0.1.11:6222",
		Routes:         []string{},
		Servers:        []string{"10.0.1.10:4222"},
	}, peer("node-a", "10.0.1.10")), config.RolePrimary)
	require.NoError(t, err)
	require.False(t, client.HostsStorage, "node-b does not store the journal")
	require.Equal(t, []string{"10.0.1.10:4222"}, client.Servers,
		"it reaches the journal on the storage node's server")
	require.Empty(t, client.Routes, "it has no server, so it clusters with nobody")
	require.Empty(t, client.ClientAddress, "it binds nothing")
	require.Empty(t, client.ClusterAddress)
}

func TestDefaultConfigForALargerSiteClustersTheStorageNodes(t *testing.T) {
	cfg, err := DefaultConfig(descriptorFor("node-c", "10.0.1.12", config.Nats{
		ClientAddress:  "10.0.1.12:4222",
		ClusterAddress: "10.0.1.12:6222",
		Servers:        []string{"10.0.1.12:4222", "10.0.1.10:4222", "10.0.1.11:4222"},
		Routes:         []string{"10.0.1.10:6222", "10.0.1.11:6222"},
	}, peer("node-a", "10.0.1.10"), peer("node-b", "10.0.1.11"), peer("node-d", "10.0.1.13")), config.RolePrimary)
	require.NoError(t, err)

	require.True(t, cfg.HostsStorage, "node-c is among the first three by name")
	require.Equal(t, 3, cfg.Replicas)
	require.Equal(t, []string{"10.0.1.10:6222", "10.0.1.11:6222"}, cfg.Routes,
		"node-c clusters with the other two storage nodes, and not with node-d")
	require.Equal(t, []string{"10.0.1.12:4222", "10.0.1.10:4222", "10.0.1.11:4222"}, cfg.Servers,
		"a storage node prefers its own server and retains peers for standby connectivity")
}

func TestDefaultConfigLeavesOutStorageForALaterNode(t *testing.T) {
	cfg, err := DefaultConfig(descriptorFor("node-d", "10.0.1.13", config.Nats{
		ClientAddress:  "10.0.1.13:4222",
		ClusterAddress: "10.0.1.13:6222",
		Routes:         []string{},
		Servers:        []string{"10.0.1.10:4222", "10.0.1.11:4222", "10.0.1.12:4222"},
	}, peer("node-a", "10.0.1.10"), peer("node-b", "10.0.1.11"), peer("node-c", "10.0.1.12")), config.RolePrimary)
	require.NoError(t, err)
	require.False(t, cfg.HostsStorage, "node-d is the fourth by name and runs no server")
	require.Empty(t, cfg.Routes, "it runs no server, so it binds no cluster listener")
	require.Equal(t, []string{"10.0.1.10:4222", "10.0.1.11:4222", "10.0.1.12:4222"}, cfg.Servers,
		"it reaches the journal on any of the site's storage nodes")
}

func loopbackStorageConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		ClientName:        "node-a",
		ServerName:        "node-a",
		ClusterName:       "site",
		ClientAddress:     "127.0.0.1:4222",
		ClusterAddress:    "127.0.0.1:6222",
		Servers:           []string{"127.0.0.1:4222"},
		HostsStorage:      true,
		JetStreamStoreDir: filepath.Join(t.TempDir(), "nats"),
		Replicas:          1,
		MaxBytes:          DefaultMaxBytes,
		MaxMessageBytes:   DefaultMaxMessageBytes,
		AckWait:           DefaultAckWait,
		MaxDeliver:        DefaultMaxDeliver,
		StartupTimeout:    DefaultStartupTimeout,
		CatchUpTimeout:    DefaultCatchUpTimeout,
		ShutdownTimeout:   DefaultShutdownTimeout,
	}
}

func TestValidateAcceptsALoopbackStorageConfig(t *testing.T) {
	require.NoError(t, loopbackStorageConfig(t).Validate())
}

func TestValidateRejectsIncompleteConfigs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{name: "no server to connect to", mutate: func(c *Config) { c.Servers = nil }, want: "the site has no storage node"},
		{name: "blank client address", mutate: func(c *Config) { c.ClientAddress = "" }, want: "client address is required"},
		{name: "blank cluster address", mutate: func(c *Config) { c.ClusterAddress = "" }, want: "cluster address is required"},
		{name: "a storage node that does not use its own server", mutate: func(c *Config) {
			c.Servers = []string{"10.0.1.11:4222"}
		}, want: "must connect to its own server"},
		{name: "a client node with routes", mutate: func(c *Config) {
			c.HostsStorage = false
			c.Routes = []string{"10.0.1.11:6222"}
		}, want: "has no cluster to route to"},
		{name: "colliding addresses", mutate: func(c *Config) { c.ClusterAddress = c.ClientAddress }, want: "used more than once"},
		{name: "route points at self", mutate: func(c *Config) { c.Routes = []string{c.ClusterAddress} }, want: "is this node itself"},
		{name: "duplicate routes", mutate: func(c *Config) { c.Routes = []string{"10.0.1.11:6222", "10.0.1.11:6222"} }, want: "listed twice"},
		{name: "storage without data dir", mutate: func(c *Config) { c.JetStreamStoreDir = "" }, want: "data directory is required"},
		{name: "non-positive max bytes", mutate: func(c *Config) { c.MaxBytes = 0 }, want: "max bytes must be positive"},
		{name: "non-positive max message bytes", mutate: func(c *Config) { c.MaxMessageBytes = 0 }, want: "max message bytes must be positive"},
		{name: "non-positive replicas", mutate: func(c *Config) { c.Replicas = 0 }, want: "replicas must be positive"},
		{name: "non-positive ack wait", mutate: func(c *Config) { c.AckWait = 0 }, want: "ack wait must be positive"},
		{name: "non-positive max deliver", mutate: func(c *Config) { c.MaxDeliver = 0 }, want: "max deliver must be positive"},
		{name: "non-positive startup timeout", mutate: func(c *Config) { c.StartupTimeout = 0 }, want: "startup timeout must be positive"},
		{name: "non-positive catch-up timeout", mutate: func(c *Config) { c.CatchUpTimeout = 0 }, want: "catch-up timeout must be positive"},
		{name: "non-positive shutdown timeout", mutate: func(c *Config) { c.ShutdownTimeout = 0 }, want: "shutdown timeout must be positive"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := loopbackStorageConfig(t)
			test.mutate(&cfg)
			require.ErrorContains(t, cfg.Validate(), test.want)
		})
	}
}

func TestValidateRequiresCredentialsOffLoopback(t *testing.T) {
	cfg := loopbackStorageConfig(t)
	cfg.ClientAddress = "10.0.1.10:4222"
	cfg.Servers = []string{"10.0.1.10:4222"}
	require.ErrorContains(t, cfg.Validate(), "username and password are required")

	cfg.Username = "opdl-site"
	cfg.Password = "secret"
	require.NoError(t, cfg.Validate(), "credentials satisfy a non-loopback address")
}

func TestValidateRejectsAnUnwritableDataDir(t *testing.T) {
	cfg := loopbackStorageConfig(t)
	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(blocked, []byte("x"), 0o644))
	cfg.JetStreamStoreDir = filepath.Join(blocked, "nats")
	require.ErrorContains(t, cfg.Validate(), "create data directory")
}

func TestDefaultDurationsArePositive(t *testing.T) {
	for _, d := range []time.Duration{DefaultStartupTimeout, DefaultCatchUpTimeout, DefaultShutdownTimeout, DefaultAckWait} {
		require.Positive(t, d)
	}
}
