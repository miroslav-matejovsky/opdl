package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/api"
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

	Machine:        "node",
	MachineProfile: "all-in-one",
	IP:             "127.0.0.1",
	Services:       []string{"core-services"},
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
// It sets no socket topology, no API address, and no runtime directory. Every one
// of those is the deployment's rather than the site's, and this file cannot move
// them: a runtime that could would be able to point a machine at a journal that
// is not its own, or give a machine's two instances one endpoint. Tests that need
// free ports move the descriptor instead, through descriptorOnFreePorts.
func writeConfig(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "config.toml")
	contents := fmt.Sprintf(`read_header_timeout = "5s"
shutdown_timeout = "10s"
lag_bound = "30s"
[event_fabric.nats]
data_dir = %q
startup_timeout = "30s"
catch_up_timeout = "30s"
`, filepath.ToSlash(filepath.Join(dir, "nats")))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
	return path
}

// descriptorOnFreePorts returns the embedded descriptor with every endpoint moved
// onto reserved loopback ports and every runtime directory into the test's own
// space.
//
// The embedded mock names the deployment's real ports and paths, which several
// tests running at once cannot all take. Moving them here rather than in the
// runtime configuration keeps the contract intact: the descriptor is still the
// single source of the machine's topology, and each instance still reads only its
// own record.
//
// Both instances are filled in, whether or not a test deploys the standby. A test
// that enables it then flips one bool rather than composing a second endpoint set
// by hand, which is how a standby ends up on the primary's address.
func descriptorOnFreePorts(t *testing.T, cfg *config.Config) deployment.Descriptor {
	t.Helper()
	descriptor := cfg.Descriptor()
	runtimeRoot := t.TempDir()
	for _, standby := range []bool{false, true} {
		instance := descriptor.Instances.Get(deployment.Role(standby))
		client, cluster := freeAddress(t), freeAddress(t)
		instance.Nats = &deployment.Nats{
			ClientAddress:  client,
			ClusterAddress: cluster,
			Servers:        []string{client},
			Routes:         []string{},
		}
		instance.APIAddress = freeAddress(t)
		instance.RuntimeDir = filepath.Join(runtimeRoot, string(deployment.Role(standby)))
		if standby {
			descriptor.Instances.Standby = instance
			continue
		}
		descriptor.Instances.Primary = instance
	}
	if descriptor.Lock == nil {
		descriptor.Lock = &deployment.Lock{}
	}
	descriptor.Lock.WindowsMutex = uniqueLockMutex(t)
	return descriptor
}

// uniqueLockMutex returns an ownership mutex name no other test or run shares.
//
// The embedded mock descriptor names one ownership mutex, and ownership is a
// kernel object in a machine-wide namespace, so every test in this binary would
// otherwise contend for the same ownership. The ports above are moved for the same
// reason; the lock needs it more, because a lock file was isolated for free by
// each test's temporary directory and a kernel object is not.
func uniqueLockMutex(t *testing.T) string {
	t.Helper()
	token := make([]byte, 8)
	_, err := rand.Read(token)
	require.NoError(t, err)
	return "opdl-app-test." + hex.EncodeToString(token)
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

// TestInstanceServerServesUntilShutdown checks the listener an instance holds for
// its whole lifetime: it answers as soon as it is open, and stops answering once
// it has drained. An orderly shutdown is not an error.
func TestInstanceServerServesUntilShutdown(t *testing.T) {
	addr := freeAddress(t)
	server, err := openInstanceServer(t.Context(), addr, time.Second, okHandler())
	require.NoError(t, err)

	require.Eventually(t, func() bool { return get(t.Context(), addr) }, 10*time.Second, 20*time.Millisecond,
		"server never became reachable")

	require.NoError(t, server.shutdown(10*time.Second), "an orderly shutdown is not an error")
	require.False(t, get(t.Context(), addr), "server still accepts requests after shutdown")
	require.NoError(t, server.shutdown(10*time.Second), "shutdown is idempotent")
}

// TestInstanceServerFailsToOpenOnATakenAddress checks a bind failure is reported
// where it happens: at startup, before anything else is composed.
//
// This is what binding at startup buys. The listener used to open on activation,
// after the Event Fabric was up, so an unusable address meant tearing a composed
// site back down — and it meant discovering the problem at a failover rather than
// at a start. Now there is nothing to release, because nothing has been opened.
func TestInstanceServerFailsToOpenOnATakenAddress(t *testing.T) {
	var listen net.ListenConfig
	listener, err := listen.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()

	_, err = openInstanceServer(t.Context(), listener.Addr().String(), time.Second, okHandler())
	require.ErrorContains(t, err, "listen on", "a listener that cannot bind must report why")
}

// TestInstanceServerSwapsHandlerWithoutRebinding is the property activation
// depends on: the address never changes, only what answers on it.
//
// A Passive instance that bound nothing and opened its listener on activation
// would leave its address free for the length of the handover, and anything else
// on the host could take it.
func TestInstanceServerSwapsHandlerWithoutRebinding(t *testing.T) {
	addr := freeAddress(t)
	server, err := openInstanceServer(t.Context(), addr, time.Second, body(http.StatusOK, "passive"))
	require.NoError(t, err)
	defer func() { _ = server.shutdown(10 * time.Second) }()

	require.Eventually(t, func() bool { return read(t, addr) == "passive" }, 10*time.Second, 20*time.Millisecond)

	server.serveWith(body(http.StatusOK, "active"))
	require.Equal(t, "active", read(t, addr), "the swap takes effect on the same listener")
	require.Equal(t, addr, server.address, "activation must not move the endpoint")
}

// okHandler answers every request, so a test can tell a running listener from a
// stopped one.
func okHandler() http.Handler { return body(http.StatusOK, "ok") }

func body(status int, text string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(text))
	})
}

// read returns addr's response body.
func read(t *testing.T, addr string) string {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr, http.NoBody)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return ""
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return string(data)
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
		Instances: deployment.Instances{
			Primary: deployment.Instance{Disabled: false, Nats: &deployment.Nats{
				ClientAddress:  "10.0.1.10:4222",
				ClusterAddress: "10.0.1.10:6222",
				Routes:         []string{},
				Servers:        []string{"10.0.1.10:4222"},
			}},
			Standby: deployment.Instance{Disabled: true},
		},
		Peers: []deployment.Peer{
			{Site: "north", Machine: "node-a", Role: deployment.RolePrimary, IP: "10.0.1.10"},
			{Site: "north", Machine: "node-b", Role: deployment.RolePrimary, IP: "10.0.1.11"},
		},
	}
	settings := func(t *testing.T, nats string) *config.Config {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, "config.toml")
		contents := "read_header_timeout = \"5s\"\nshutdown_timeout = \"11s\"\nlag_bound = \"30s\"\n[event_fabric.nats]\n" + nats
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
		Peers: []deployment.Peer{
			{Site: "north", Machine: "node-a", Role: deployment.RolePrimary, IP: "10.0.1.10"},
			{Site: "north", Machine: "node-b", Role: deployment.RolePrimary, IP: "10.0.1.11"},
			// node-b deploys a standby as well. It is a second member of the
			// fabric but not a second confirmation: exactly one of a machine's
			// instances is Active, and it answers for the machine.
			{Site: "north", Machine: "node-b", Role: deployment.RoleStandby, IP: "10.0.1.11"},
		},
	})
	require.Equal(t, registration.Location{Machine: "node-a", IP: "10.0.1.10"}, self)
	require.Equal(t, []registration.Location{
		{Machine: "node-a", IP: "10.0.1.10"},
		{Machine: "node-b", IP: "10.0.1.11"},
	}, expected, "a machine confirms its own registrations too, and once per machine")

	self, expected = topology(testDescriptor)
	require.Equal(t, []registration.Location{self}, expected,
		"a one-machine site expects only itself")
}

// TestResolveRole checks role selection against the warm-standby policy.
func TestResolveRole(t *testing.T) {
	cases := map[string]struct {
		instance    string
		warmStandby bool
		want        redundancy.InstanceRole
		wantErr     string
	}{
		"opt-out requires a role":      {instance: "", warmStandby: false, wantErr: "-instance primary|standby is required"},
		"opt-out accepts primary":      {instance: "primary", warmStandby: false, want: redundancy.RolePrimary},
		"opt-out rejects standby":      {instance: "standby", warmStandby: false, wantErr: "does not run a warm standby"},
		"warm standby requires a role": {instance: "", warmStandby: true, wantErr: "-instance primary|standby is required"},
		"warm standby accepts primary": {instance: "primary", warmStandby: true, want: redundancy.RolePrimary},
		"warm standby accepts standby": {instance: "standby", warmStandby: true, want: redundancy.RoleStandby},
		"invalid role":                 {instance: "other", warmStandby: true, wantErr: "invalid instance role"},
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
	require.False(t, status.FailoverReady)
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

// TestFailoverAndFailback exercises both ownership transfers.
// The service-manager action is represented by canceling the active process only
// after the waiting process reports a caught-up standby status.
func TestFailoverAndFailback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping failover integration test in -short mode")
	}
	dir := t.TempDir()
	cfg, err := config.Load(writeConfig(t, dir))
	require.NoError(t, err)
	descriptor := descriptorOnFreePorts(t, cfg)
	// The standby is enabled and nothing else changes. The descriptor now resolves
	// a NATS topology per instance, but the runtime has not yet been changed to
	// start the standby's own server: it is still turned client-only and follows
	// the address the Active instance is serving on. See nats.DefaultConfig.
	descriptor.Instances.Standby.Disabled = false

	// Each instance has its own API address, so a transfer moves which address
	// answers rather than moving one address between processes.
	primaryAPI := descriptor.Instances.Primary.APIAddress
	standbyAPI := descriptor.Instances.Standby.APIAddress
	require.NotEqual(t, primaryAPI, standbyAPI)

	primaryCtx, stopPrimary := context.WithCancel(t.Context())
	primaryDone := make(chan error, 1)
	go func() { primaryDone <- runProcess(primaryCtx, cfg, descriptor, redundancy.RolePrimary) }()
	waitForProcessState(t, descriptor, redundancy.RolePrimary, redundancy.StateActive, primaryDone)
	require.True(t, get(t.Context(), primaryAPI), "the preferred primary did not serve")
	require.False(t, get(t.Context(), standbyAPI), "nothing is on the standby's address before it starts")
	requireInstance(t, primaryAPI, "primary", api.InstanceStateActive)

	standbyCtx, stopStandby := context.WithCancel(t.Context())
	standbyDone := make(chan error, 1)
	go func() { standbyDone <- runProcess(standbyCtx, cfg, descriptor, redundancy.RoleStandby) }()
	waitForFailoverReadyStandby(t, descriptor, redundancy.RoleStandby, standbyDone)

	// The Passive instance binds its own address for its whole lifetime, so an
	// operator can ask it about itself while the other instance is the one
	// serving. It answers for itself and refuses domain operations.
	requireInstance(t, standbyAPI, "standby", api.InstanceStatePassive)
	requirePassiveRefusal(t, standbyAPI, primaryAPI)

	stopPrimary()
	require.NoError(t, waitProcess(t, primaryDone), "the primary did not stop cleanly")
	waitForProcessState(t, descriptor, redundancy.RoleStandby, redundancy.StateActive, standbyDone)
	// The Standby Instance serves on its own address, not the one the Primary
	// Instance was on. Nothing binds the stopped instance's address.
	require.True(t, get(t.Context(), standbyAPI), "the Standby Instance did not restore the API after failover")
	require.False(t, get(t.Context(), primaryAPI), "the Standby Instance took over the Primary Instance's address")
	// Same address, same listener, different answer: activation swapped the
	// handler rather than moving the endpoint.
	requireInstance(t, standbyAPI, "standby", api.InstanceStateActive)

	failbackCtx, stopFailback := context.WithCancel(t.Context())
	failbackDone := make(chan error, 1)
	go func() { failbackDone <- runProcess(failbackCtx, cfg, descriptor, redundancy.RolePrimary) }()
	waitForFailoverReadyStandby(t, descriptor, redundancy.RolePrimary, failbackDone)

	// Failback is operator-initiated: deployment keeps the Primary Instance
	// preferred by gracefully stopping the Active Standby, and only after the
	// returning Primary Instance is caught up.
	stopStandby()
	require.NoError(t, waitProcess(t, standbyDone), "the Active Standby did not stop cleanly")
	waitForProcessState(t, descriptor, redundancy.RolePrimary, redundancy.StateActive, failbackDone)
	require.True(t, get(t.Context(), primaryAPI), "the Primary Instance did not restore the API after failback")
	require.False(t, get(t.Context(), standbyAPI), "the stopped Standby Instance's address is still answering")

	stopFailback()
	require.NoError(t, waitProcess(t, failbackDone), "the Primary Instance did not stop cleanly")
}

func waitForProcessState(t *testing.T, descriptor deployment.Descriptor, role redundancy.InstanceRole, state redundancy.State, done <-chan error) {
	t.Helper()
	path := redundancy.StatusPath(instanceOf(descriptor, role).RuntimeDir)
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

func waitForFailoverReadyStandby(t *testing.T, descriptor deployment.Descriptor, role redundancy.InstanceRole, done <-chan error) {
	t.Helper()
	path := redundancy.StatusPath(instanceOf(descriptor, role).RuntimeDir)
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
		return err == nil && status.State == redundancy.StatePassive && status.FailoverReady && status.LastError == ""
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
	contents := fmt.Sprintf(`read_header_timeout = "5s"
shutdown_timeout = "10s"
lag_bound = "30s"
[event_fabric.nats]
data_dir = %q
startup_timeout = "30s"
catch_up_timeout = "30s"
`, filepath.ToSlash(blocked))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))

	err := Run([]string{"-config", path, "-instance", "primary"})
	require.ErrorContains(t, err, "data directory")
	require.ErrorContains(t, err, "nats:", "the failure names the storage it could not use")
}

// requireInstance checks the instance serving at addr reports the role and state
// it should. It is how a test asks an instance what it is, which is the same
// question an operator asks it.
func requireInstance(t *testing.T, addr, role, state string) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+api.PathInstance, http.NoBody)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, http.StatusOK, response.StatusCode)

	var instance api.Instance
	require.NoError(t, json.NewDecoder(response.Body).Decode(&instance))
	require.Equal(t, role, instance.Role, "the role is fixed at build time")
	require.Equal(t, state, instance.State)
	require.Equal(t, addr, instance.Address)
}

// requirePassiveRefusal checks a Passive instance refuses a domain operation and
// points at the instance that holds ownership, so a caller that reached the wrong
// one can follow it rather than give up.
func requirePassiveRefusal(t *testing.T, passiveAddr, activeAddr string) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		"http://"+passiveAddr+api.PathRegistrations, http.NoBody)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()

	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode,
		"a Passive instance must not answer a domain query from a projection that is not authoritative")
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), activeAddr, "the refusal names where to go instead")
}
