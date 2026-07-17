package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/platform/embedded"
	"github.com/miroslav-matejovsky/opdl/platform/internal/config"
	natsfabric "github.com/miroslav-matejovsky/opdl/platform/internal/eventfabric/nats"
	"github.com/miroslav-matejovsky/opdl/platform/internal/registration"
)

// This file covers what runtime composition owns: deriving the Event Fabric's
// configuration from the deployment, placing a node's storage, reading the
// trusted topology, and the startup and shutdown ordering.
//
// The composition tests run a real embedded NATS server, so they are integration
// tests and stay out of the fast gate. Everything derivable without a socket is
// tested without one.

var testDescriptor = deployment.Descriptor{
	Platform:    "opdl",
	Project:     "scenario",
	Environment: "development",
	Site:        "local",
	Machine:     "node",
	Role:        "all-in-one",
	IP:          "127.0.0.1",
	Services:    []string{"core-services"},
	EventFabric: deployment.EventFabric{},
}

// freeAddress reserves an ephemeral loopback port, then releases it so the
// server under test can bind it.
func freeAddress(t *testing.T) string {
	t.Helper()
	var listen net.ListenConfig
	listener, err := listen.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())
	return addr
}

// newTestServer builds a server that answers on addr, so a test can tell a
// running platform from a stopped one.
func newTestServer(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: time.Second}
}

// get reports whether addr answers an HTTP request.
func get(ctx context.Context, addr string) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr, http.NoBody)
	if err != nil {
		return false
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return false
	}
	_ = response.Body.Close()
	return true
}

// writeConfig writes a loopback platform configuration whose Event Fabric binds
// free ports and stores its journal under the test's own directory, so several
// tests can run at once without colliding.
func writeConfig(t *testing.T, dir string) string {
	t.Helper()
	client, cluster, monitor := freeAddress(t), freeAddress(t), freeAddress(t)
	path := filepath.Join(dir, "config.toml")
	contents := fmt.Sprintf(`address = %q
read_header_timeout = "5s"
shutdown_timeout = "10s"
[event_fabric.nats]
data_dir = %q
startup_timeout = "30s"
catch_up_timeout = "30s"
client_address = %q
cluster_address = %q
monitor_address = %q
`, freeAddress(t), filepath.ToSlash(filepath.Join(dir, "nats")), client, cluster, monitor)
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
	return path
}

// embeddedDescriptor is the identity this test binary was compiled with. A
// platform's identity is not configurable, so a composition test reads it rather
// than choosing it.
func embeddedDescriptor(t *testing.T) deployment.Descriptor {
	t.Helper()
	d, err := embedded.Deployment()
	require.NoError(t, err)
	return d
}

// openTestSite composes a real Event Fabric on loopback and returns it ready to
// serve, as Run would. It skips in the fast gate: a site is not a site without a
// journal, and a journal means a server and a disk.
func openTestSite(t *testing.T) *site {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Event Fabric composition in -short mode")
	}
	cfg, err := config.Load(writeConfig(t, t.TempDir()))
	require.NoError(t, err)

	s, err := open(t.Context(), cfg.Descriptor(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.close(context.Background()) })
	return s
}

// TestOpenIsReadyBeforeItReturns checks the promise the startup order exists
// for: a returned site has already caught its projection up to the journal and
// stated its readiness, so the first request cannot arrive before the node has
// seen the site's history.
func TestOpenIsReadyBeforeItReturns(t *testing.T) {
	s := openTestSite(t)

	require.True(t, s.ready, "a site that returned from open has announced it is ready")
	state, err := s.fabric.State(t.Context())
	require.NoError(t, err)
	require.True(t, state.Connected)
	require.True(t, state.CaughtUp, "the projection has applied everything the journal held")
	require.Positive(t, state.HighWater, "the node's own ready event is in the journal")
	require.GreaterOrEqual(t, s.projection.Sequence(), state.HighWater,
		"the node applied its own readiness before it returned")
}

// TestOpenStatesReadyIntoTheJournal checks readiness is a fact in the site's
// history rather than a log line: it names the node's transport, journal, and
// storage role, and reports the sequence the node had caught up to.
func TestOpenStatesReadyIntoTheJournal(t *testing.T) {
	s := openTestSite(t)

	// The node names itself from the descriptor it was compiled with, which for a
	// test binary is the neutral mock the builder stages over.
	info := s.fabric.Info()
	require.Equal(t, natsfabric.Name, info.Adapter)
	require.Equal(t, embeddedDescriptor(t).Machine, info.Server)
	require.NotEmpty(t, info.Journal)
	require.True(t, info.HostsStorage, "the only machine of a one-machine site stores its journal")
	require.Equal(t, 1, info.Replicas)
}

// TestSiteServesAndReleasesOnSignal checks the whole ordered lifecycle: the
// server answers, an interrupt stops it, and the site releases everything it
// composed without reporting a failure. An orderly shutdown is not an error.
func TestSiteServesAndReleasesOnSignal(t *testing.T) {
	s := openTestSite(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	addr := freeAddress(t)

	stopped := make(chan error, 1)
	go func() { stopped <- serve(ctx, newTestServer(addr), s, 10*time.Second) }()
	require.Eventually(t, func() bool { return get(ctx, addr) }, 10*time.Second, 20*time.Millisecond,
		"server never became reachable")

	// Canceling the context is what an interrupt does to the runtime.
	cancel()
	select {
	case err := <-stopped:
		require.NoError(t, err, "an orderly shutdown is not an error")
	case <-time.After(20 * time.Second):
		t.Fatal("serve did not return after its context was canceled")
	}
	require.False(t, get(t.Context(), addr), "server still accepts requests after shutdown")
}

// TestServeReleasesTheSiteWhenTheServerCannotStart checks the failure path
// releases what startup composed: a platform that cannot listen must not leave a
// NATS server and its store running behind it.
func TestServeReleasesTheSiteWhenTheServerCannotStart(t *testing.T) {
	s := openTestSite(t)

	// Hold the address so ListenAndServe fails immediately.
	var listen net.ListenConfig
	listener, err := listen.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()

	err = serve(t.Context(), newTestServer(listener.Addr().String()), s, 10*time.Second)
	require.ErrorContains(t, err, "serve HTTP", "a server that cannot start must report why")

	// The transport really is released, not just reported as released.
	_, err = s.fabric.HighWater(t.Context())
	require.Error(t, err, "the fabric is closed even on the failure path")
}

// TestCloseIsIdempotent checks a site can be released twice. serve releases on
// every path, and a test or a caller that also releases must not turn an orderly
// shutdown into an error.
func TestCloseIsIdempotent(t *testing.T) {
	s := openTestSite(t)
	require.NoError(t, s.close(t.Context()))
	require.NoError(t, s.close(t.Context()), "closing an already closed site is not a failure")
}

// TestNatsConfigDerivesFromDescriptorAndAppliesOverrides pins the precedence
// rule: the deployment decides, and the configuration file may move sockets and
// place storage.
func TestNatsConfigDerivesFromDescriptorAndAppliesOverrides(t *testing.T) {
	descriptor := deployment.Descriptor{
		Project: "customer-a", Environment: "production",
		Site: "north", Machine: "node-a", IP: "10.0.1.10",
		EventFabric: deployment.EventFabric{
			Peers: []deployment.EventFabricPeer{{Site: "north", Machine: "node-b", IP: "10.0.1.11"}},
		},
	}
	settings := func(t *testing.T, nats string) *config.Config {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, "config.toml")
		contents := "address = \"127.0.0.1:8080\"\nread_header_timeout = \"5s\"\nshutdown_timeout = \"11s\"\n[event_fabric.nats]\n" + nats
		require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
		cfg, err := config.Load(path)
		require.NoError(t, err)
		return cfg
	}
	const required = "data_dir = \"/var/lib/opdl\"\nstartup_timeout = \"45s\"\ncatch_up_timeout = \"25s\"\n"

	t.Run("no overrides uses the deployment", func(t *testing.T) {
		cfg, err := natsConfig(descriptor, settings(t, required))
		require.NoError(t, err)
		require.Equal(t, "10.0.1.10:4222", cfg.ClientAddress)
		require.Equal(t, "10.0.1.10:6222", cfg.ClusterAddress)
		require.Equal(t, []string{"10.0.1.10:4222"}, cfg.Servers,
			"node-a is the site's only storage node, so it reaches the journal on its own server")
		require.Empty(t, cfg.Routes, "the site's only server has nobody to cluster with")
		require.Equal(t, 45*time.Second, cfg.StartupTimeout)
		require.Equal(t, 25*time.Second, cfg.CatchUpTimeout)
		require.Equal(t, 11*time.Second, cfg.ShutdownTimeout, "the fabric closes within the runtime's own bound")
	})

	t.Run("overrides move sockets", func(t *testing.T) {
		cfg, err := natsConfig(descriptor, settings(t, required+`client_address = "127.0.0.1:4001"
cluster_address = "127.0.0.1:4002"
monitor_address = "127.0.0.1:4003"
routes = ["127.0.0.1:4102"]
servers = ["127.0.0.1:4001"]
`))
		require.NoError(t, err)
		require.Equal(t, "127.0.0.1:4001", cfg.ClientAddress)
		require.Equal(t, "127.0.0.1:4002", cfg.ClusterAddress)
		require.Equal(t, "127.0.0.1:4003", cfg.MonitorAddress)
		require.Equal(t, []string{"127.0.0.1:4102"}, cfg.Routes)
		require.Equal(t, []string{"127.0.0.1:4001"}, cfg.Servers)
	})

	t.Run("a partial override keeps the rest of the deployment", func(t *testing.T) {
		cfg, err := natsConfig(descriptor, settings(t, required+"client_address = \"127.0.0.1:4001\"\n"))
		require.NoError(t, err)
		require.Equal(t, "127.0.0.1:4001", cfg.ClientAddress)
		require.Equal(t, "10.0.1.10:6222", cfg.ClusterAddress, "an absent override is not a blank")
		require.Equal(t, "127.0.0.1:8222", cfg.MonitorAddress)
	})

	t.Run("a storage node always reaches the journal on its own server", func(t *testing.T) {
		// node-a stores the site journal, so where it connects follows where its
		// own server listens. It is not a second setting that could disagree.
		cfg, err := natsConfig(descriptor, settings(t,
			required+"client_address = \"127.0.0.1:4001\"\nservers = [\"10.9.9.9:4222\"]\n"))
		require.NoError(t, err)
		require.Equal(t, []string{"127.0.0.1:4001"}, cfg.Servers,
			"a storage node cannot be configured to run one journal and talk to another")
	})

	t.Run("storage and replicas come from the site, not the file", func(t *testing.T) {
		cfg, err := natsConfig(descriptor, settings(t, required))
		require.NoError(t, err)
		require.True(t, cfg.HostsStorage, "node-a sorts first in a two-machine site")
		require.Equal(t, 1, cfg.Replicas, "a site smaller than three machines runs one replica")
	})

	t.Run("credentials come from their own file", func(t *testing.T) {
		dir := t.TempDir()
		secrets := filepath.Join(dir, "creds.toml")
		require.NoError(t, os.WriteFile(secrets, []byte("username = \"opdl\"\npassword = \"s3cret\"\n"), 0o600))
		cfg, err := natsConfig(descriptor, settings(t, required+fmt.Sprintf("credentials_file = %q\n", filepath.ToSlash(secrets))))
		require.NoError(t, err)
		require.Equal(t, "opdl", cfg.Username)
		require.Equal(t, "s3cret", cfg.Password)
	})
}

// TestNodeDataDirIsNamedAfterTheMachine checks two machines sharing one
// configured data directory never share a store. A JetStream store carries a
// server's identity, so two nodes in one directory would claim each other's
// journal.
func TestNodeDataDirIsNamedAfterTheMachine(t *testing.T) {
	dataDir := t.TempDir()
	nodeA := nodeDataDir(dataDir, deployment.Descriptor{
		Project: "customer-a", Environment: "production", Site: "north", Machine: "node-a",
	})
	nodeB := nodeDataDir(dataDir, deployment.Descriptor{
		Project: "customer-a", Environment: "production", Site: "north", Machine: "node-b",
	})
	require.Equal(t, filepath.Join(dataDir, "customer-a-production-north-node-a"), nodeA)
	require.NotEqual(t, nodeA, nodeB, "two machines of one site never share a store")

	otherSite := nodeDataDir(dataDir, deployment.Descriptor{
		Project: "customer-a", Environment: "production", Site: "south", Machine: "node-a",
	})
	require.NotEqual(t, nodeA, otherSite, "the same machine name in another site is another node")
}

// TestTopologyExpectsEverySiteMachineIncludingItself checks the trusted
// registration topology is the descriptor's static membership. Acceptance needs
// every expected machine, so the set must never be "who is reachable".
func TestTopologyExpectsEverySiteMachineIncludingItself(t *testing.T) {
	self, expected := topology(deployment.Descriptor{
		Site: "north", Machine: "node-a", IP: "10.0.1.10",
		EventFabric: deployment.EventFabric{
			Peers: []deployment.EventFabricPeer{{Site: "north", Machine: "node-b", IP: "10.0.1.11"}},
		},
	})
	require.Equal(t, registration.Location{Machine: "node-a", IP: "10.0.1.10"}, self)
	require.Equal(t, []registration.Location{
		{Machine: "node-a", IP: "10.0.1.10"},
		{Machine: "node-b", IP: "10.0.1.11"},
	}, expected, "a machine confirms its own registrations too")

	self, expected = topology(testDescriptor)
	require.Equal(t, []registration.Location{self}, expected,
		"a one-machine site expects only itself")
}

func TestRunReportsMissingConfigFlag(t *testing.T) {
	require.Error(t, Run([]string{"-unknown"}))
}

func TestRunReportsUnusableConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte("[invalid"), 0o644))
	require.ErrorContains(t, Run([]string{"-config", path}), "invalid configuration file")
}

// TestRunReportsUnusableJournalStorage checks a node fails at startup rather
// than when its first event needs writing. The journal is the site's history: a
// platform that cannot store it must not start and pretend otherwise.
func TestRunReportsUnusableJournalStorage(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "not-a-dir")
	require.NoError(t, os.WriteFile(blocked, []byte("x"), 0o644))

	path := filepath.Join(dir, "config.toml")
	contents := fmt.Sprintf(`address = "127.0.0.1:8080"
read_header_timeout = "5s"
shutdown_timeout = "10s"
[event_fabric.nats]
data_dir = %q
startup_timeout = "30s"
catch_up_timeout = "30s"
`, filepath.ToSlash(blocked))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))

	err := Run([]string{"-config", path})
	require.ErrorContains(t, err, "data directory")
	require.ErrorContains(t, err, "nats:", "the failure names the storage it could not use")
}
