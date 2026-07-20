package nats

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
)

// descriptorFor builds a machine's descriptor the way the resolver would: the
// machine's own addresses, the topology it resolved, and the peers that make up
// its site.
func descriptorFor(machine, ip string, nats deployment.EventFabricNats, peers ...deployment.EventFabricPeer) deployment.Descriptor {
	return deployment.Descriptor{
		Project: "customer-a", Environment: "production", Site: "north",
		Machine: machine, IP: ip,
		Slots: deployment.Slots{
			Primary: deployment.Slot{Disabled: false},
			Standby: deployment.Slot{Disabled: false},
		},
		EventFabric: deployment.EventFabric{Nats: nats, Peers: peers},
	}
}

func peer(machine, ip string) deployment.EventFabricPeer {
	return deployment.EventFabricPeer{Site: "north", Machine: machine, IP: ip}
}

func TestStorageNodesSelectsBySortedName(t *testing.T) {
	tests := []struct {
		name     string
		machines []string
		want     []string
	}{
		{name: "empty", machines: nil, want: nil},
		{name: "one machine hosts storage", machines: []string{"node-a"}, want: []string{"node-a"}},
		{name: "two machines use one storage node", machines: []string{"node-b", "node-a"}, want: []string{"node-a"}},
		{name: "three machines all host storage, sorted", machines: []string{"node-c", "node-a", "node-b"}, want: []string{"node-a", "node-b", "node-c"}},
		{name: "larger site uses the first three by name", machines: []string{"node-d", "node-a", "node-c", "node-b"}, want: []string{"node-a", "node-b", "node-c"}},
		{name: "duplicates are compacted", machines: []string{"node-a", "node-a", "node-b"}, want: []string{"node-a"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, StorageNodes(test.machines))
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
	cfg, err := DefaultConfig(descriptorFor("node-a", "10.0.1.10", deployment.EventFabricNats{
		ClientAddress:  "10.0.1.10:4222",
		ClusterAddress: "10.0.1.10:6222",
		Routes:         []string{},
		Servers:        []string{"10.0.1.10:4222"},
	}))
	require.NoError(t, err)

	require.True(t, cfg.HostsStorage, "the only machine hosts storage")
	require.Equal(t, 1, cfg.Replicas)
	require.Empty(t, cfg.Routes, "a single-node site has no other storage node to cluster with")
	require.Equal(t, "10.0.1.10:4222", cfg.ClientAddress)
	require.Equal(t, "10.0.1.10:6222", cfg.ClusterAddress)
	require.Equal(t, "node-a", cfg.ServerName)
	require.NotEmpty(t, cfg.ClusterName)
	require.Equal(t, []string{"10.0.1.10:4222"}, cfg.Servers, "a storage node reaches the journal on its own server")
}

// TestDefaultConfigTakesNoProcessRole checks every process on a machine composes
// the same endpoints from the same descriptor.
//
// This is the regression for the warm standby failure. When the standby derived
// its own NATS configuration it got an address the active process was not
// serving on, so it retried forever against a port nothing was listening on.
// There is one topology per machine now, and the only role-dependent step is
// clientOnly, which removes ownership rather than choosing a different endpoint.
func TestDefaultConfigTakesNoProcessRole(t *testing.T) {
	descriptor := descriptorFor("node-a", "10.0.1.10", deployment.EventFabricNats{
		ClientAddress:  "10.0.1.10:4222",
		ClusterAddress: "10.0.1.10:6222",
		Routes:         []string{},
		Servers:        []string{"10.0.1.10:4222"},
	})

	first, err := DefaultConfig(descriptor)
	require.NoError(t, err)
	second, err := DefaultConfig(descriptor)
	require.NoError(t, err)
	require.Equal(t, first, second, "one machine, one topology, whatever the process role")
	require.Equal(t, "10.0.1.10:4222", second.Servers[0],
		"the server list points at the address the fence owner is serving on")
}

// TestDefaultConfigForATwoMachineSiteRunsOneServer pins the POC topology. Two
// machines run one storage node, and the other machine does not run a server at
// all: it is a client of the one that does.
//
// The alternative would be a second server that holds no journal. NATS sizes a
// journal's metadata group from the cluster it is in, so that server would join
// the group deciding whether the site can write while having nowhere to write
// to, and the site would need both machines up to accept anything. One server
// and one client is what keeps a two-machine site working when the second
// machine is down.
func TestDefaultConfigForATwoMachineSiteRunsOneServer(t *testing.T) {
	storage, err := DefaultConfig(descriptorFor("node-a", "10.0.1.10", deployment.EventFabricNats{
		ClientAddress:  "10.0.1.10:4222",
		ClusterAddress: "10.0.1.10:6222",
		Routes:         []string{},
		Servers:        []string{"10.0.1.10:4222"},
	}, peer("node-b", "10.0.1.11")))
	require.NoError(t, err)
	require.True(t, storage.HostsStorage, "node-a sorts first")
	require.Empty(t, storage.Routes, "the site's only server has nobody to cluster with")
	require.Equal(t, []string{"10.0.1.10:4222"}, storage.Servers)

	client, err := DefaultConfig(descriptorFor("node-b", "10.0.1.11", deployment.EventFabricNats{
		ClientAddress:  "10.0.1.11:4222",
		ClusterAddress: "10.0.1.11:6222",
		Routes:         []string{},
		Servers:        []string{"10.0.1.10:4222"},
	}, peer("node-a", "10.0.1.10")))
	require.NoError(t, err)
	require.False(t, client.HostsStorage, "node-b does not store the journal")
	require.Equal(t, []string{"10.0.1.10:4222"}, client.Servers,
		"it reaches the journal on the storage node's server")
	require.Empty(t, client.Routes, "it has no server, so it clusters with nobody")
	// The descriptor resolves addresses for every machine, but a machine that
	// stores nothing binds none of them.
	require.Empty(t, client.ClientAddress, "it binds nothing")
	require.Empty(t, client.ClusterAddress)
}

// TestDefaultConfigForALargerSiteClustersTheStorageNodes checks the cluster is
// exactly the storage nodes. A fourth machine is a client of the three, and none
// of the three routes to it: routing to a server that holds no journal would
// only enlarge the metadata group's quorum without enlarging its storage.
func TestDefaultConfigForALargerSiteClustersTheStorageNodes(t *testing.T) {
	cfg, err := DefaultConfig(descriptorFor("node-c", "10.0.1.12", deployment.EventFabricNats{
		ClientAddress:  "10.0.1.12:4222",
		ClusterAddress: "10.0.1.12:6222",
		Servers:        []string{"10.0.1.12:4222", "10.0.1.10:4222", "10.0.1.11:4222"},
		Routes:         []string{"10.0.1.10:6222", "10.0.1.11:6222"},
	}, peer("node-a", "10.0.1.10"), peer("node-b", "10.0.1.11"), peer("node-d", "10.0.1.13")))
	require.NoError(t, err)

	// Four machines: the first three by sorted name host storage. node-c is one of
	// them; node-d is not.
	require.True(t, cfg.HostsStorage, "node-c is among the first three by name")
	require.Equal(t, 3, cfg.Replicas)
	require.Equal(t, []string{"10.0.1.10:6222", "10.0.1.11:6222"}, cfg.Routes,
		"node-c clusters with the other two storage nodes, and not with node-d")
	require.Equal(t, []string{"10.0.1.12:4222", "10.0.1.10:4222", "10.0.1.11:4222"}, cfg.Servers,
		"a storage node prefers its own server and retains peers for standby connectivity")
}

func TestDefaultConfigLeavesOutStorageForALaterNode(t *testing.T) {
	cfg, err := DefaultConfig(descriptorFor("node-d", "10.0.1.13", deployment.EventFabricNats{
		ClientAddress:  "10.0.1.13:4222",
		ClusterAddress: "10.0.1.13:6222",
		Routes:         []string{},
		Servers:        []string{"10.0.1.10:4222", "10.0.1.11:4222", "10.0.1.12:4222"},
	}, peer("node-a", "10.0.1.10"), peer("node-b", "10.0.1.11"), peer("node-c", "10.0.1.12")))
	require.NoError(t, err)
	require.False(t, cfg.HostsStorage, "node-d is the fourth by name and runs no server")
	require.Empty(t, cfg.Routes, "it runs no server, so it binds no cluster listener")
	require.Equal(t, []string{"10.0.1.10:4222", "10.0.1.11:4222", "10.0.1.12:4222"}, cfg.Servers,
		"it reaches the journal on any of the site's storage nodes")
}

// loopbackStorageConfig is a valid single-node storage configuration on loopback
// with a writable data directory. Validation tests break one field of it.
func loopbackStorageConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		ServerName:      "node-a",
		ClusterName:     "site",
		ClientAddress:   "127.0.0.1:4222",
		ClusterAddress:  "127.0.0.1:6222",
		Servers:         []string{"127.0.0.1:4222"},
		HostsStorage:    true,
		DataDir:         filepath.Join(t.TempDir(), "nats"),
		Replicas:        1,
		MaxBytes:        DefaultMaxBytes,
		MaxMessageBytes: DefaultMaxMessageBytes,
		AckWait:         DefaultAckWait,
		MaxDeliver:      DefaultMaxDeliver,
		StartupTimeout:  DefaultStartupTimeout,
		CatchUpTimeout:  DefaultCatchUpTimeout,
		ShutdownTimeout: DefaultShutdownTimeout,
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
		{name: "storage without data dir", mutate: func(c *Config) { c.DataDir = "" }, want: "data directory is required"},
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
	// A file where a directory must be cannot become one.
	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(blocked, []byte("x"), 0o644))
	cfg.DataDir = filepath.Join(blocked, "nats")
	require.ErrorContains(t, cfg.Validate(), "create data directory")
}

// durationsArePositive is a small guard so the default durations stay sane.
func TestDefaultDurationsArePositive(t *testing.T) {
	for _, d := range []time.Duration{DefaultStartupTimeout, DefaultCatchUpTimeout, DefaultShutdownTimeout, DefaultAckWait} {
		require.Positive(t, d)
	}
}
