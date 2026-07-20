package nats

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestServerOptionsConfigureNoMonitoringListener checks the embedded server is
// asked for no HTTP monitoring listener.
//
// Zero is what makes NATS start none, so this asserts the absence of a port
// rather than the presence of a setting. The runtime reads connection, journal
// high-water, projection progress, and lag through the Event Fabric client API
// and writes them to its status files, so the monitor would be an extra open
// port carrying signals the platform already owns.
func TestServerOptionsConfigureNoMonitoringListener(t *testing.T) {
	opts, err := serverOptions(Config{
		ServerName:    "node",
		ClusterName:   "site",
		ClientAddress: "127.0.0.1:4222",
		HostsStorage:  true,
	})
	require.NoError(t, err)
	require.Zero(t, opts.HTTPPort, "NATS must start no HTTP monitoring listener")
	require.Zero(t, opts.HTTPSPort, "NATS must start no HTTPS monitoring listener")
	require.Empty(t, opts.HTTPHost)
}

// TestServerOptionsBindTheClientListener checks the client listener is always
// configured on a storage node: it is how the active process, the local
// standby, and every non-storage machine of the site reach the journal.
func TestServerOptionsBindTheClientListener(t *testing.T) {
	opts, err := serverOptions(Config{
		ServerName:    "node",
		ClusterName:   "site",
		ClientAddress: "10.0.1.10:4222",
		HostsStorage:  true,
	})
	require.NoError(t, err)
	require.Equal(t, "10.0.1.10", opts.Host)
	require.Equal(t, 4222, opts.Port)
	require.True(t, opts.JetStream)
}

// TestServerOptionsBindTheClusterListenerOnlyWithRoutes checks a configured
// cluster address is not by itself permission to bind it.
//
// A site whose topology selects one storage node has no peer server. Binding a
// cluster listener there would open a port nothing can connect to, so the
// listener follows the resolved routes rather than the authored port.
func TestServerOptionsBindTheClusterListenerOnlyWithRoutes(t *testing.T) {
	base := Config{
		ServerName:     "node",
		ClusterName:    "site",
		ClientAddress:  "10.0.1.10:4222",
		ClusterAddress: "10.0.1.10:6222",
		HostsStorage:   true,
	}

	t.Run("no routes", func(t *testing.T) {
		opts, err := serverOptions(base)
		require.NoError(t, err)
		require.Zero(t, opts.Cluster.Port, "a site with one storage node binds no cluster listener")
		require.Empty(t, opts.Routes)
	})

	t.Run("with routes", func(t *testing.T) {
		cfg := base
		cfg.Routes = []string{"10.0.1.11:6222", "10.0.1.12:6222"}
		opts, err := serverOptions(cfg)
		require.NoError(t, err)
		require.Equal(t, "10.0.1.10", opts.Cluster.Host)
		require.Equal(t, 6222, opts.Cluster.Port)
		require.Equal(t, "site", opts.Cluster.Name)
		require.Len(t, opts.Routes, 2)
		require.Equal(t, "nats://10.0.1.11:6222", opts.Routes[0].String())
	})
}
