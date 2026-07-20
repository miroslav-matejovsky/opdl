package app

import (
	"context"
	"errors"
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
	"github.com/miroslav-matejovsky/opdl/platform/internal/eventfabric"
	natsfabric "github.com/miroslav-matejovsky/opdl/platform/internal/eventfabric/nats"
	"github.com/miroslav-matejovsky/opdl/platform/internal/redundancy"
	"github.com/miroslav-matejovsky/opdl/platform/internal/registration"
	"github.com/miroslav-matejovsky/opdl/utils/testnet"
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
	res, err := testnet.Reserve(t.Context(), 1)
	require.NoError(t, err)
	require.NoError(t, res.Release())
	return res.Addresses()[0]
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

// writeConfig writes a loopback platform configuration that stores its journal
// and coordination state under the test's own directory, so several tests can
// run at once without colliding.
//
// It sets no socket topology. The Event Fabric's addresses are the deployment's,
// not the site's, and this file cannot move them: a runtime that could would be
// able to point a machine at a journal that is not its own. Tests that need free
// ports move the descriptor instead, through descriptorOnFreePorts.
func writeConfig(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "config.toml")
	contents := fmt.Sprintf(`address = %q
read_header_timeout = "5s"
shutdown_timeout = "10s"
instance_dir = %q
lag_bound = "30s"
[event_fabric.nats]
data_dir = %q
startup_timeout = "30s"
catch_up_timeout = "30s"
`, freeAddress(t), filepath.ToSlash(filepath.Join(dir, "instance")),
		filepath.ToSlash(filepath.Join(dir, "nats")))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
	return path
}

// descriptorOnFreePorts returns the embedded descriptor with its Event Fabric
// addresses moved onto reserved loopback ports.
//
// The embedded mock names the deployment's real ports, which several tests
// running at once cannot all bind. Moving them here rather than in the runtime
// configuration keeps the contract intact: the descriptor is still the single
// source of the machine's topology, and both processes of the machine still
// derive the same endpoints from it.
func descriptorOnFreePorts(t *testing.T, cfg *config.Config) deployment.Descriptor {
	t.Helper()
	descriptor := cfg.Descriptor()
	client, cluster := freeAddress(t), freeAddress(t)
	descriptor.EventFabric.Nats.ClientAddress = client
	descriptor.EventFabric.Nats.ClusterAddress = cluster
	descriptor.EventFabric.Nats.Servers = []string{client}
	descriptor.EventFabric.Nats.Routes = []string{}
	return descriptor
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

	s, err := open(t.Context(), descriptorOnFreePorts(t, cfg), cfg, true, redundancy.RolePrimary)
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

// TestNatsConfigDerivesFromDescriptor pins the ownership rule: the deployment
// descriptor owns the Event Fabric's topology, and the configuration file owns
// only the machine's own runtime concerns, such as where storage lives, how long
// startup may take, and where credentials are read from.
//
// The file cannot move a socket. Doing so could point a machine at a journal
// that is not its own, and nothing downstream would be able to tell.
func TestNatsConfigDerivesFromDescriptor(t *testing.T) {
	descriptor := deployment.Descriptor{
		Project: "customer-a", Environment: "production",
		Site: "north", Machine: "node-a", IP: "10.0.1.10",
		Slots: deployment.Slots{
			Primary: deployment.Slot{Disabled: false},
			Standby: deployment.Slot{Disabled: false},
		},
		EventFabric: deployment.EventFabric{
			Nats: deployment.EventFabricNats{
				ClientAddress:  "10.0.1.10:4222",
				ClusterAddress: "10.0.1.10:6222",
				Routes:         []string{},
				Servers:        []string{"10.0.1.10:4222"},
			},
			Peers: []deployment.EventFabricPeer{{Site: "north", Machine: "node-b", IP: "10.0.1.11"}},
		},
	}
	settings := func(t *testing.T, nats string) *config.Config {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, "config.toml")
		contents := "address = \"127.0.0.1:8080\"\nread_header_timeout = \"5s\"\nshutdown_timeout = \"11s\"\ninstance_dir = \"/var/lib/opdl/instance\"\nlag_bound = \"30s\"\n[event_fabric.nats]\n" + nats
		require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
		cfg, err := config.Load(path)
		require.NoError(t, err)
		return cfg
	}
	const required = "data_dir = \"/var/lib/opdl\"\nstartup_timeout = \"45s\"\ncatch_up_timeout = \"25s\"\n"

	t.Run("endpoints come from the deployment", func(t *testing.T) {
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

func TestClientOnlyRetainsEveryStorageServer(t *testing.T) {
	cfg := clientOnly(natsfabric.Config{
		HostsStorage:  true,
		ClientAddress: "10.0.1.10:4222",
		Servers:       []string{"10.0.1.10:4222", "10.0.1.11:4222", "10.0.1.12:4222"},
		DataDir:       "journal",
	})

	require.False(t, cfg.HostsStorage)
	require.Empty(t, cfg.ClientAddress)
	require.Empty(t, cfg.DataDir)
	require.Equal(t, []string{"10.0.1.10:4222", "10.0.1.11:4222", "10.0.1.12:4222"}, cfg.Servers)
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

// TestResolveRole checks role selection against the warm-standby policy.
func TestResolveRole(t *testing.T) {
	cases := map[string]struct {
		instance    string
		warmStandby bool
		want        redundancy.ProcessRole
		wantErr     string
	}{
		"opt-out requires a role":      {instance: "", warmStandby: false, wantErr: "-instance primary|standby is required"},
		"opt-out accepts primary":      {instance: "primary", warmStandby: false, want: redundancy.RolePrimary},
		"opt-out rejects standby":      {instance: "standby", warmStandby: false, wantErr: "does not run a warm standby"},
		"warm standby requires a role": {instance: "", warmStandby: true, wantErr: "-instance primary|standby is required"},
		"warm standby accepts primary": {instance: "primary", warmStandby: true, want: redundancy.RolePrimary},
		"warm standby accepts standby": {instance: "standby", warmStandby: true, want: redundancy.RoleStandby},
		"invalid role":                 {instance: "other", warmStandby: true, wantErr: "invalid process role"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := resolveRole(tc.instance, tc.warmStandby)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

type fixedStatusFabric struct {
	state eventfabric.State
	err   error
}

func (f fixedStatusFabric) State(context.Context) (eventfabric.State, error) {
	return f.state, f.err
}

// TestStartStatusFailsBeforeRuntimeStarts checks a process never serves while its
// initial status cannot be written. Shutdown and handover tooling must not be
// given a stale operational view.
func TestStartStatusFailsBeforeRuntimeStarts(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(blocked, []byte("x"), 0o600))

	done, err := startStatus(
		t.Context(),
		fixedStatusFabric{state: eventfabric.State{Connected: true, CaughtUp: true}},
		redundancy.RolePrimary,
		redundancy.StateActive,
		filepath.Join(blocked, "primary.status"),
		30*time.Second,
		nil,
		nil,
	)
	require.ErrorContains(t, err, "write status")
	require.Nil(t, done)
}

// TestStartStatusStopsServingAfterFabricStateFailures checks loss of the Event
// Fabric cannot leave an active process serving an indefinitely stale view.
func TestStartStatusStopsServingAfterFabricStateFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	statusPath := filepath.Join(t.TempDir(), "primary.status")
	lagExceeded := make(chan struct{}, 1)
	done, err := startStatus(
		ctx,
		fixedStatusFabric{err: errors.New("event fabric disconnected")},
		redundancy.RolePrimary,
		redundancy.StateActive,
		statusPath,
		100*time.Millisecond,
		func() { lagExceeded <- struct{}{} },
		nil,
	)
	require.NoError(t, err)

	select {
	case <-lagExceeded:
	case <-time.After(2 * statusInterval):
		require.FailNow(t, "fabric state failure never exceeded the lag bound")
	}
	status, err := redundancy.ReadStatus(statusPath)
	require.NoError(t, err)
	require.False(t, status.Promotable)
	require.NotEqual(t, unknownLag, status.Lag)
	require.Contains(t, status.LastError, "event fabric disconnected")

	cancel()
	require.NoError(t, <-done)
}

// TestActiveAndStandbyRunTogether checks one all-in-one machine can run two
// processes against one journal while only the active
// owns active capabilities, the standby is client-only and produces nothing, and
// the standby catches up and follows new journal events.
func TestActiveAndStandbyRunTogether(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping redundant Event Fabric composition in -short mode")
	}
	cfg, err := config.Load(writeConfig(t, t.TempDir()))
	require.NoError(t, err)
	descriptor := descriptorOnFreePorts(t, cfg)

	active, err := open(t.Context(), descriptor, cfg, true, redundancy.RolePrimary)
	require.NoError(t, err)
	t.Cleanup(func() { _ = active.close(context.Background()) })
	require.True(t, active.ready, "the active process announces readiness")
	require.True(t, active.fabric.Info().HostsStorage, "the active process owns the journal store")

	standby, err := open(t.Context(), descriptor, cfg, false, redundancy.RoleStandby)
	require.NoError(t, err)
	t.Cleanup(func() { _ = standby.close(context.Background()) })

	// The standby holds no active capability: client-only transport, no handler,
	// no command or query service, and it never announces readiness.
	require.False(t, standby.fabric.Info().HostsStorage, "a warm standby opens no journal store or listener")
	require.Nil(t, standby.commands, "a standby exposes no command service")
	require.Nil(t, standby.queries, "a standby exposes no query service")
	require.Empty(t, standby.services, "a standby attaches no durable handler")
	require.False(t, standby.ready, "a standby never announces readiness")

	// The standby caught up to the journal the active is on.
	state, err := standby.fabric.State(t.Context())
	require.NoError(t, err)
	require.True(t, state.CaughtUp, "the standby caught up to the journal")
	require.Positive(t, state.Applied, "the standby applied the active's readiness")

	// It follows new journal events: a fact published on the active reaches the
	// standby's projection.
	receipt, err := active.fabric.Publish(t.Context(), eventfabric.NewReady(active.fabric.Info(), 0, redundancy.RolePrimary.String()))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		st, stateErr := standby.fabric.State(t.Context())
		return stateErr == nil && st.Applied >= receipt.Sequence
	}, 10*time.Second, 20*time.Millisecond, "the standby did not follow a new journal event")
}

// TestPromotionAndPrimaryReclamation exercises both ownership transfers.
// The service-manager action is represented by canceling the active process only
// after the waiting process reports a caught-up standby status.
func TestPromotionAndPrimaryReclamation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping promotion integration test in -short mode")
	}
	dir := t.TempDir()
	cfg, err := config.Load(writeConfig(t, dir))
	require.NoError(t, err)
	descriptor := descriptorOnFreePorts(t, cfg)
	// The standby is enabled and nothing else changes. It shares the machine's
	// one NATS topology with the active process, so it connects to the address
	// the active process is serving on and, once promoted, rebinds that same
	// address rather than moving the site onto a second one.
	descriptor.Slots.Standby = deployment.Slot{Disabled: false}

	primaryCtx, stopPrimary := context.WithCancel(t.Context())
	primaryDone := make(chan error, 1)
	go func() { primaryDone <- runProcess(primaryCtx, cfg, descriptor, redundancy.RolePrimary) }()
	waitForProcessState(t, cfg, descriptor, redundancy.RolePrimary, redundancy.StateActive, primaryDone)
	require.True(t, get(t.Context(), cfg.Address()), "the preferred primary did not serve")

	standbyCtx, stopStandby := context.WithCancel(t.Context())
	standbyDone := make(chan error, 1)
	go func() { standbyDone <- runProcess(standbyCtx, cfg, descriptor, redundancy.RoleStandby) }()
	waitForPromotableStandby(t, cfg, descriptor, redundancy.RoleStandby, standbyDone)

	stopPrimary()
	require.NoError(t, waitProcess(t, primaryDone), "the primary did not stop cleanly")
	waitForProcessState(t, cfg, descriptor, redundancy.RoleStandby, redundancy.StateActive, standbyDone)
	require.True(t, get(t.Context(), cfg.Address()), "the promoted standby did not restore the API")

	reclaimCtx, stopReclaim := context.WithCancel(t.Context())
	reclaimDone := make(chan error, 1)
	go func() { reclaimDone <- runProcess(reclaimCtx, cfg, descriptor, redundancy.RolePrimary) }()
	waitForPromotableStandby(t, cfg, descriptor, redundancy.RolePrimary, reclaimDone)

	// Deployment keeps the primary preferred by gracefully stopping the promoted
	// standby only after the returning primary is caught up.
	stopStandby()
	require.NoError(t, waitProcess(t, standbyDone), "the promoted standby did not stop cleanly")
	waitForProcessState(t, cfg, descriptor, redundancy.RolePrimary, redundancy.StateActive, reclaimDone)
	require.True(t, get(t.Context(), cfg.Address()), "the reclaimed primary did not restore the API")

	stopReclaim()
	require.NoError(t, waitProcess(t, reclaimDone), "the reclaimed primary did not stop cleanly")
}

func waitForProcessState(t *testing.T, cfg *config.Config, descriptor deployment.Descriptor, role redundancy.ProcessRole, state redundancy.State, done <-chan error) {
	t.Helper()
	path := redundancy.StatusPath(cfg.InstanceDir(), descriptor.Project, descriptor.Environment, descriptor.Site, descriptor.Machine, role)
	var processErr error
	exited := false
	require.Eventually(t, func() bool {
		select {
		case err := <-done:
			exited, processErr = true, err
			return true
		default:
		}
		status, err := redundancy.ReadStatus(path)
		return err == nil && status.State == state
	}, 30*time.Second, 20*time.Millisecond, "%s never reached %s", role, state)
	require.Falsef(t, exited, "%s exited before reaching %s: %v", role, state, processErr)
}

func waitForPromotableStandby(t *testing.T, cfg *config.Config, descriptor deployment.Descriptor, role redundancy.ProcessRole, done <-chan error) {
	t.Helper()
	path := redundancy.StatusPath(cfg.InstanceDir(), descriptor.Project, descriptor.Environment, descriptor.Site, descriptor.Machine, role)
	var processErr error
	exited := false
	require.Eventually(t, func() bool {
		select {
		case err := <-done:
			exited, processErr = true, err
			return true
		default:
		}
		status, err := redundancy.ReadStatus(path)
		return err == nil && status.State == redundancy.StateStandby && status.Promotable && status.LastError == ""
	}, 30*time.Second, 20*time.Millisecond, "%s never became a caught-up standby", role)
	require.Falsef(t, exited, "%s exited before becoming a caught-up standby: %v", role, processErr)
}

func waitProcess(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(30 * time.Second):
		t.Fatal("process did not stop")
		return nil
	}
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
instance_dir = %q
lag_bound = "30s"
[event_fabric.nats]
data_dir = %q
startup_timeout = "30s"
catch_up_timeout = "30s"
`, filepath.ToSlash(filepath.Join(dir, "instance")), filepath.ToSlash(blocked))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))

	err := Run([]string{"-config", path, "-instance", "primary"})
	require.ErrorContains(t, err, "data directory")
	require.ErrorContains(t, err, "nats:", "the failure names the storage it could not use")
}
