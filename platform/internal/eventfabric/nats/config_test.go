package nats

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
)

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
	cfg, err := DefaultConfig(deployment.Descriptor{
		Project: "customer-a", Environment: "production", Site: "north",
		Machine: "node-a", IP: "10.0.1.10",
	})
	require.NoError(t, err)

	require.True(t, cfg.HostsStorage, "the only machine hosts storage")
	require.Equal(t, 1, cfg.Replicas)
	require.Empty(t, cfg.Routes, "a single-node site has no peers to route to")
	require.Equal(t, "10.0.1.10:4222", cfg.ClientAddress)
	require.Equal(t, "10.0.1.10:6222", cfg.ClusterAddress)
	require.Equal(t, "127.0.0.1:8222", cfg.MonitorAddress, "monitoring binds to loopback by default")
	require.Equal(t, "node-a", cfg.ServerName)
	require.NotEmpty(t, cfg.ClusterName)
}

func TestDefaultConfigForALargerSiteDerivesStorageAndRoutes(t *testing.T) {
	descriptor := deployment.Descriptor{
		Project: "customer-a", Environment: "production", Site: "north",
		Machine: "node-c", IP: "10.0.1.12",
		Fabric: deployment.Fabric{Peers: []deployment.FabricPeer{
			{Site: "north", Machine: "node-a", IP: "10.0.1.10"},
			{Site: "north", Machine: "node-b", IP: "10.0.1.11"},
			{Site: "north", Machine: "node-d", IP: "10.0.1.13"},
		}},
	}

	cfg, err := DefaultConfig(descriptor)
	require.NoError(t, err)

	// Four machines: the first three by sorted name host storage. node-c is one of
	// them; node-d is not.
	require.True(t, cfg.HostsStorage, "node-c is among the first three by name")
	require.Equal(t, 3, cfg.Replicas)
	require.Equal(t, []string{"10.0.1.10:6222", "10.0.1.11:6222", "10.0.1.13:6222"}, cfg.Routes)
}

func TestDefaultConfigLeavesOutStorageForALaterNode(t *testing.T) {
	descriptor := deployment.Descriptor{
		Project: "customer-a", Environment: "production", Site: "north",
		Machine: "node-d", IP: "10.0.1.13",
		Fabric: deployment.Fabric{Peers: []deployment.FabricPeer{
			{Site: "north", Machine: "node-a", IP: "10.0.1.10"},
			{Site: "north", Machine: "node-b", IP: "10.0.1.11"},
			{Site: "north", Machine: "node-c", IP: "10.0.1.12"},
		}},
	}

	cfg, err := DefaultConfig(descriptor)
	require.NoError(t, err)
	require.False(t, cfg.HostsStorage, "node-d is the fourth by name and runs Core NATS only")
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
		MonitorAddress:  "127.0.0.1:8222",
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
		{name: "blank client address", mutate: func(c *Config) { c.ClientAddress = "" }, want: "client address is required"},
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
