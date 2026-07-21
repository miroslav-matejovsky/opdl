package nats

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/config"
)

// descriptorFor builds a machine's descriptor the way the resolver would: the
// machine's own addresses, the topology it resolved, and the peers that make up
// its site.
func descriptorFor(machine, ip string, nats config.Nats, peers ...config.Peer) config.Descriptor {
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

// descriptorWithStandby builds a storage machine that deploys both instances,
// each with its own topology and its own store, the way the resolver does. It is
// what a machine looks like once every instance runs its own server.
func descriptorWithStandby(machine, ip string, primary, standby config.Nats, peers ...config.Peer) config.Descriptor {
	d := descriptorFor(machine, ip, primary, peers...)
	d.Instances.Standby = config.Instance{
		Disabled: false,
		DataDir:  "/var/lib/opdl/" + machine + "/standby",
		Nats:     &standby,
	}
	// A machine deploying both contributes two peers to its site, so the standby
	// is one of them, and storage selection sees it as a candidate in its own
	// right.
	d.Peers = append(d.Peers, config.Peer{
		Site: "north", Machine: machine, Role: config.RoleStandby, IP: ip,
		Nats: config.PeerNats{ClientAddress: standby.ClientAddress, ClusterAddress: standby.ClusterAddress},
	})
	return d
}

// peer is another machine's Primary Instance. Storage is selected per instance,
// so a fixture that gives each machine one instance makes the machine count and
// the instance count the same, which is what these older cases assume.
func peer(machine, ip string) config.Peer {
	return config.Peer{Site: "north", Machine: machine, Role: config.RolePrimary, IP: ip}
}

// peerInstance is one named instance of another machine, with the addresses its
// own server binds. It is how a fixture builds a site whose machines deploy both
// instances, which is the minimum redundant shape.
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
		// The minimum redundant site: two machines that each deploy a standby.
		// Four instances give a three-member cluster, which two machines counted
		// as machines could never do.
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
	// The name carries the role even where the machine deploys one instance. Two
	// servers in one cluster cannot share a name, and which instances a machine
	// deploys is not something the site's other machines derive names from.
	require.Equal(t, "node-a-primary", cfg.ServerName)
	// The client name is the instance's too. A machine opens two connections now,
	// and naming both after the machine would make them indistinguishable in the
	// diagnostics the name exists for.
	require.Equal(t, "node-a-primary", cfg.ClientName)
	require.NotEmpty(t, cfg.ClusterName)
	require.Equal(t, []string{"10.0.1.10:4222"}, cfg.Servers, "a storage node reaches the journal on its own server")
}

// TestDefaultConfigReadsTheRunningInstancesOwnTopology checks each instance of a
// machine composes its own endpoints, its own store, and its own server name.
//
// This test used to assert the opposite: that a machine had one topology
// whatever the process role. That was correct while one server served a whole
// machine and the standby was made client-only, and it was the guard against the
// warm standby defect, where a standby derived an endpoint the active process was
// not serving on and retried forever against a port nothing was listening on.
//
// The guard is now the other way round. Both instances run a server, so reading
// one instance's topology for both would make the two contend for one set of
// ports, and the second would fail to start. What must not happen has not
// changed: an instance must never be pointed at an address that is not its own.
func TestDefaultConfigReadsTheRunningInstancesOwnTopology(t *testing.T) {
	// The minimum redundant site: two machines that each deploy a standby. Four
	// instances is what makes a three-member cluster possible, and three members
	// is what lets the site lose one and keep quorum. A single machine with a
	// standby is two instances, which is below that and stores on one of them.
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

	// Each binds its own addresses and neither binds the other's. This is the
	// property the whole stage exists to establish.
	require.Equal(t, "10.0.1.10:4222", primary.ClientAddress)
	require.Equal(t, "10.0.1.10:4322", standby.ClientAddress)
	require.Equal(t, "10.0.1.10:6222", primary.ClusterAddress)
	require.Equal(t, "10.0.1.10:6322", standby.ClusterAddress)

	// And each routes to the other, so a machine's two servers are peers in the
	// site's cluster like any other pair.
	require.Equal(t, []string{"10.0.1.10:6322", "10.0.1.11:6222"}, primary.Routes)
	require.Equal(t, []string{"10.0.1.10:6222", "10.0.1.11:6222"}, standby.Routes)

	// Two servers in one cluster cannot share a name, and two servers on one host
	// cannot open one store.
	require.Equal(t, "node-a-primary", primary.ServerName)
	require.Equal(t, "node-a-standby", standby.ServerName)
	require.NotEqual(t, primary.DataDir, standby.DataDir)
	require.Equal(t, "/var/lib/opdl/node-a/primary", primary.DataDir)
	require.Equal(t, "/var/lib/opdl/node-a/standby", standby.DataDir)

	// Four instances means a replicated journal, which two machines counted as
	// machines could never have reached.
	require.Equal(t, 3, primary.Replicas)
	require.Equal(t, 3, standby.Replicas)

	// Each prefers its own server, so neither depends on the other to reach the
	// journal it is itself storing.
	require.Equal(t, "10.0.1.10:4222", primary.Servers[0])
	require.Equal(t, "10.0.1.10:4322", standby.Servers[0])
}

// TestDefaultConfigRejectsAnInstanceTheMachineDoesNotDeploy guards the role
// argument. Asking for a standby's topology on a machine that deploys none is a
// composition mistake, and the descriptor carries nothing to answer it with.
func TestDefaultConfigRejectsAnInstanceTheMachineDoesNotDeploy(t *testing.T) {
	_, err := DefaultConfig(descriptorFor("node-a", "10.0.1.10", config.Nats{
		ClientAddress:  "10.0.1.10:4222",
		ClusterAddress: "10.0.1.10:6222",
		Routes:         []string{},
		Servers:        []string{"10.0.1.10:4222"},
	}), config.RoleStandby)
	require.ErrorContains(t, err, "does not deploy the standby instance")
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
	cfg, err := DefaultConfig(descriptorFor("node-c", "10.0.1.12", config.Nats{
		ClientAddress:  "10.0.1.12:4222",
		ClusterAddress: "10.0.1.12:6222",
		Servers:        []string{"10.0.1.12:4222", "10.0.1.10:4222", "10.0.1.11:4222"},
		Routes:         []string{"10.0.1.10:6222", "10.0.1.11:6222"},
	}, peer("node-a", "10.0.1.10"), peer("node-b", "10.0.1.11"), peer("node-d", "10.0.1.13")), config.RolePrimary)
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

// loopbackStorageConfig is a valid single-node storage configuration on loopback
// with a writable data directory. Validation tests break one field of it.
func loopbackStorageConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		ClientName:      "node-a",
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
