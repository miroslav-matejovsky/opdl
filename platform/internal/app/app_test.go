package app

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage/eventfabric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage/jsonl"
	natsbackend "github.com/miroslav-matejovsky/opdl/platform/internal/events/storage/nats"
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

var testDescriptor = config.Descriptor{
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
// It sets no socket topology, no API address, no runtime directory, and no data
// directory. Every one of those is the deployment's rather than the site's, and
// this file cannot move them: a runtime that could would be able to point a
// machine at a journal that is not its own, or give a machine's two instances one
// endpoint or one store. Tests that need free ports and private directories move
// the descriptor instead, through descriptorOnFreePorts.
func writeConfig(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "config.toml")
	contents := `read_header_timeout = "5s"
shutdown_timeout = "10s"
lag_bound = "30s"
[event_fabric.nats]
startup_timeout = "30s"
catch_up_timeout = "30s"
`
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
func descriptorOnFreePorts(t *testing.T, cfg *config.Config) config.Descriptor {
	t.Helper()
	descriptor := cfg.Descriptor()
	dataRoot := t.TempDir()
	for _, standby := range []bool{false, true} {
		instance := descriptor.Instances.Get(config.Role(standby))
		client, cluster := freeAddress(t), freeAddress(t)
		instanceDataDir := filepath.Join(dataRoot, string(config.Role(standby)))
		instance.Nats = &config.Nats{
			JetStreamStoreDir: filepath.Join(instanceDataDir, "eventfabric", "nats"),
			ClientAddress:     client,
			ClusterAddress:    cluster,
			Servers:           []string{client},
			Routes:            []string{},
		}
		instance.APIAddress = freeAddress(t)
		instance.DataDir = instanceDataDir
		if standby {
			descriptor.Instances.Standby = instance
			continue
		}
		descriptor.Instances.Primary = instance
	}
	if descriptor.Lock == nil {
		descriptor.Lock = &config.Lock{}
	}
	descriptor.Lock.WindowsMutex = uniqueLockMutex(t)
	return descriptor
}

// deployStandby turns a descriptor whose instances are already on free ports into
// the shape the resolver produces for a single machine that deploys both.
//
// That shape is not two clustered servers, and the difference matters. Storage is
// selected per instance, and a lone machine deploying both is a site of two
// instances, which is below the three a replicated journal needs. So the resolver
// selects one storage instance, the primary, and the standby is a client of it
// with no server, no store, and no routes.
//
// Redundancy that survives losing a storage instance needs four instances: two
// machines that each deploy a standby. That is the minimum redundant site, and it
// is a scenario rather than a composition test, because it needs four processes.
//
// Getting this wrong is silent in a specific way worth naming: routing the two
// instances to each other here, as if they were both storage, gives the primary a
// route to an address nothing binds and its JetStream never reaches quorum.
func deployStandby(descriptor config.Descriptor) config.Descriptor {
	primary, standby := descriptor.Instances.Primary, descriptor.Instances.Standby
	standby.Disabled = false
	// One storage instance means nobody routes: there is no second server to
	// cluster with, so neither binds a cluster listener.
	primary.Nats.Routes = []string{}
	standby.Nats.Routes = []string{}
	primary.Nats.Servers = []string{primary.Nats.ClientAddress}
	// The standby reaches the journal on the instance that stores it.
	standby.Nats.Servers = []string{primary.Nats.ClientAddress}
	descriptor.Instances.Primary, descriptor.Instances.Standby = primary, standby
	return descriptor
}

// uniqueLockMutex returns an ownership mutex name no other test or run shares.
//
// The embedded mock descriptor names one ownership mutex, and ownership is a
// kernel object in a machine-wide namespace, so every test in this binary would
// otherwise contend for the same ownership. The ports above are moved for the same
// reason; the lock needs it more, because a lock file was isolated for free by
// each test's temporary directory and a kernel object is not.
//
// The Global\ prefix is part of the name because a descriptor's is. A composition
// test that fed OpenLock a bare name would be testing a shape the builder cannot
// produce; see TestOpenLockAcceptsTheDescriptorsQualifiedName.
func uniqueLockMutex(t *testing.T) string {
	t.Helper()
	token := make([]byte, 8)
	_, err := rand.Read(token)
	require.NoError(t, err)
	return `Global\opdl-app-test.` + hex.EncodeToString(token)
}

// embeddedDescriptor is the identity this test binary was compiled with. A
// platform's identity is not configurable, so a composition test reads it rather
// than choosing it.
func embeddedDescriptor(t *testing.T) config.Descriptor {
	t.Helper()
	d, err := config.Deployment()
	require.NoError(t, err)
	return d
}

// newTestProcess composes what Run composes before it opens anything: one
// envelope factory for the process, the mandatory local record, and the
// process-local publisher over it.
//
// It returns the composition error rather than failing the test, because the
// local record failing to open is itself something a test asserts about.
func newTestProcess(t *testing.T, descriptor config.Descriptor, cfg *config.Config, role redundancy.InstanceRole) (process, error) {
	t.Helper()
	factory, err := events.NewFactory(descriptor, role.String())
	require.NoError(t, err)
	record, err := jsonl.New(instanceOf(descriptor, role).DataDir)
	if err != nil {
		return process{}, err
	}
	t.Cleanup(func() { _ = record.Close(context.Background()) })
	local, err := storage.NewPublisher(factory, record)
	require.NoError(t, err)
	return process{
		descriptor: descriptor,
		cfg:        cfg,
		role:       role,
		factory:    factory,
		local:      local,
		record:     record,
	}, nil
}

// openSite composes a site the way Run does: one process composition, and the
// site built from it.
func openSite(t *testing.T, descriptor config.Descriptor, cfg *config.Config, active bool, role redundancy.InstanceRole) (*site, error) {
	t.Helper()
	proc, err := newTestProcess(t, descriptor, cfg, role)
	if err != nil {
		return nil, err
	}
	return open(t.Context(), proc, active)
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

	s, err := openSite(t, descriptorOnFreePorts(t, cfg), cfg, true, redundancy.RolePrimary)
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
	// test binary is the neutral mock the builder stages over. The name carries
	// the instance role: a machine runs one server per instance, and two servers
	// in one cluster cannot share a name.
	info := s.fabric.Info()
	require.Equal(t, natsbackend.Name, info.Adapter)
	require.Equal(t, embeddedDescriptor(t).Machine+"-primary", info.Server)
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
// descriptor owns the Event Fabric's topology and, since each instance runs its
// own server, its store. The configuration file owns only how long startup may
// take and where credentials are read from.
//
// The file cannot move a socket. Doing so could point a machine at a journal
// that is not its own, and nothing downstream would be able to tell.
func TestNatsConfigDerivesFromDescriptor(t *testing.T) {
	descriptor := config.Descriptor{
		Project: "customer-a", Environment: "production",
		Site: "north", Machine: "node-a", IP: "10.0.1.10",
		Instances: config.Instances{
			Primary: config.Instance{Disabled: false, DataDir: "/var/lib/opdl/node-a/primary", Nats: &config.Nats{
				JetStreamStoreDir: "/var/lib/opdl/node-a/primary/eventfabric/nats",
				ClientAddress:     "10.0.1.10:4222",
				ClusterAddress:    "10.0.1.10:6222",
				Routes:            []string{},
				Servers:           []string{"10.0.1.10:4222"},
			}},
			Standby: config.Instance{Disabled: true},
		},
		Peers: []config.Peer{
			{Site: "north", Machine: "node-a", Role: config.RolePrimary, IP: "10.0.1.10"},
			{Site: "north", Machine: "node-b", Role: config.RolePrimary, IP: "10.0.1.11"},
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
	const required = "startup_timeout = \"45s\"\ncatch_up_timeout = \"25s\"\n"

	t.Run("endpoints come from the deployment", func(t *testing.T) {
		cfg, err := natsConfig(descriptor, settings(t, required), redundancy.RolePrimary)
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
		cfg, err := natsConfig(descriptor, settings(t, required), redundancy.RolePrimary)
		require.NoError(t, err)
		require.True(t, cfg.HostsStorage, "node-a sorts first in a two-machine site")
		require.Equal(t, 1, cfg.Replicas, "a site smaller than three machines runs one replica")
	})

	t.Run("credentials come from their own file", func(t *testing.T) {
		dir := t.TempDir()
		secrets := filepath.Join(dir, "creds.toml")
		require.NoError(t, os.WriteFile(secrets, []byte("username = \"opdl\"\npassword = \"s3cret\"\n"), 0o600))
		cfg, err := natsConfig(descriptor, settings(t, required+fmt.Sprintf("credentials_file = %q\n", filepath.ToSlash(secrets))), redundancy.RolePrimary)
		require.NoError(t, err)
		require.Equal(t, "opdl", cfg.Username)
		require.Equal(t, "s3cret", cfg.Password)
	})
}

// TestNatsConfigComposesEachInstanceSeparately replaces two tests that no longer
// have subjects: one for clientOnly, which stripped a standby's server, and one
// for nodeDataDir, which derived a machine's store path from its identity.
//
// Both existed because one server served a whole machine. Now each instance runs
// its own, so nothing is stripped from a standby and no path is derived: the
// store is the instance's own, from its descriptor record. What the two tests
// were really protecting is checked here instead, on the composition rather than
// on the helpers: a machine's two instances must not end up sharing anything they
// each open.
func TestNatsConfigComposesEachInstanceSeparately(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, t.TempDir()))
	require.NoError(t, err)
	descriptor := deployStandby(descriptorOnFreePorts(t, cfg))

	primary, err := natsConfig(descriptor, cfg, redundancy.RolePrimary)
	require.NoError(t, err)
	standby, err := natsConfig(descriptor, cfg, redundancy.RoleStandby)
	require.NoError(t, err)

	// Each reads its own record: its own client name, and its own server list.
	require.NotEqual(t, primary.ClientName, standby.ClientName,
		"a machine opens two connections and they must be told apart")
	require.NotEmpty(t, primary.JetStreamStoreDir, "the storage instance stores its journal somewhere")
	require.NotEqual(t, descriptor.Instances.Primary.DataDir, descriptor.Instances.Standby.DataDir,
		"the descriptor gives each instance its own store, whichever ends up opening one")

	// On a lone machine only one instance is selected for storage, because two
	// instances is below the three a replicated journal needs. The standby is a
	// client of the instance that stores.
	require.True(t, primary.HostsStorage)
	require.False(t, standby.HostsStorage, "a site of two instances runs one storage node")
	require.Equal(t, primary.ClientAddress, standby.Servers[0],
		"the standby reaches the journal on the address the storage instance serves")
}

// TestTopologyExpectsEverySiteMachineIncludingItself checks the trusted
// registration topology is the descriptor's static membership. Acceptance needs
// every expected machine, so the set must never be "who is reachable".
func TestTopologyExpectsEverySiteMachineIncludingItself(t *testing.T) {
	self, expected := topology(config.Descriptor{
		Site: "north", Machine: "node-a", IP: "10.0.1.10",
		Peers: []config.Peer{
			{Site: "north", Machine: "node-a", Role: config.RolePrimary, IP: "10.0.1.10"},
			{Site: "north", Machine: "node-b", Role: config.RolePrimary, IP: "10.0.1.11"},
			// node-b deploys a standby as well. It is a second member of the
			// fabric but not a second confirmation: exactly one of a machine's
			// instances is Active, and it answers for the machine.
			{Site: "north", Machine: "node-b", Role: config.RoleStandby, IP: "10.0.1.11"},
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

// recordingPublisher collects what a component stated, and can be told to fail,
// so a test can read the record without a storage backend.
type recordingPublisher struct {
	mu     sync.Mutex
	err    error
	stated []events.Event
}

func (p *recordingPublisher) Publish(_ context.Context, event events.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	p.stated = append(p.stated, event)
	return nil
}

func (p *recordingPublisher) events() []events.Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.stated)
}

// TestFailoverMonitorFailsBeforeRuntimeStarts checks a process never serves while
// its readiness cannot be stated. Shutdown and handover tooling read the local
// record, and an instance missing from it cannot be handed a machine.
func TestFailoverMonitorFailsBeforeRuntimeStarts(t *testing.T) {
	publisher := &recordingPublisher{err: errors.New("record unavailable")}

	done, err := startFailoverMonitor(
		t.Context(),
		publisher,
		fixedStatusFabric{state: eventfabric.State{Connected: true, CaughtUp: true}},
		redundancy.StateActive,
		30*time.Second,
		nil,
	)
	require.ErrorContains(t, err, "record unavailable")
	require.Nil(t, done)
}

// TestFailoverMonitorStopsServingAfterFabricStateFailures checks loss of the
// Event Fabric cannot leave an active process serving an indefinitely stale
// view, and that the instance says so once rather than on every observation.
func TestFailoverMonitorStopsServingAfterFabricStateFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	publisher := &recordingPublisher{}
	lagExceeded := make(chan struct{}, 1)
	done, err := startFailoverMonitor(
		ctx,
		publisher,
		fixedStatusFabric{err: errors.New("event fabric disconnected")},
		redundancy.StateActive,
		100*time.Millisecond,
		func() {
			select {
			case lagExceeded <- struct{}{}:
			default:
			}
		},
	)
	require.NoError(t, err)

	select {
	case <-lagExceeded:
	case <-time.After(2 * monitorInterval):
		require.FailNow(t, "fabric state failure never exceeded the lag bound")
	}

	cancel()
	require.NoError(t, <-done)

	// The opening observation is stated whatever it says, and an instance that was
	// unready from the start and stayed unready has nothing more to state.
	stated := publisher.events()
	require.Len(t, stated, 1, "readiness is stated on change, not on a timer: %+v", stated)
	readiness, ok := stated[0].(FailoverReadinessChanged)
	require.True(t, ok)
	require.False(t, readiness.Ready)
	require.Equal(t, redundancy.StateActive.String(), readiness.InstanceState)
	require.Contains(t, readiness.Error, "event fabric disconnected")
}

// TestFailoverMonitorStatesEveryReadinessChange checks the record carries the
// transitions the status file used to be polled for: an instance that catches up
// says so, and one that falls behind its bound says that too.
func TestFailoverMonitorStatesEveryReadinessChange(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	publisher := &recordingPublisher{}
	fabric := &togglingFabric{state: eventfabric.State{Connected: true, CaughtUp: true, Applied: 7, HighWater: 7}}

	done, err := startFailoverMonitor(ctx, publisher, fabric, redundancy.StatePassive, time.Nanosecond, nil)
	require.NoError(t, err)

	// A projection that falls behind for longer than the bound is no longer a
	// machine anyone can be handed.
	fabric.set(eventfabric.State{Connected: true, CaughtUp: false, Applied: 7, HighWater: 9})
	require.Eventually(t, func() bool {
		return len(publisher.events()) >= 2
	}, 10*monitorInterval, monitorInterval/4)

	cancel()
	require.NoError(t, <-done)

	stated := publisher.events()
	opening := stated[0].(FailoverReadinessChanged)
	require.True(t, opening.Ready)
	require.Equal(t, redundancy.StatePassive.String(), opening.InstanceState)
	require.Equal(t, uint64(7), opening.AppliedSequence)

	lost := stated[1].(FailoverReadinessChanged)
	require.False(t, lost.Ready)
	require.Equal(t, uint64(9), lost.HighWater)
	require.Empty(t, lost.Error, "falling behind is not a failure to ask")
}

// togglingFabric is a progressFabric a test can move between states while the
// monitor is observing it.
type togglingFabric struct {
	mu    sync.Mutex
	state eventfabric.State
}

func (f *togglingFabric) set(state eventfabric.State) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = state
}

func (f *togglingFabric) State(context.Context) (eventfabric.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, nil
}

// TestActiveAndStandbyRunTogether checks one all-in-one machine can run two
// processes against one site journal while only the active owns active
// capabilities, the standby produces nothing, and the standby catches up and
// follows new journal events.
//
// What "runs together" means changed with the per-instance Event Fabric. The
// standby used to be a client of the active's server; now both run their own
// server, on their own ports and their own store, and route to each other. So
// the thing being checked is no longer that the standby is a lesser NATS
// participant. It is a full cluster member. What makes it a standby is that it
// holds no ownership: no handler, no services, no readiness.
func TestActiveAndStandbyRunTogether(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping redundant Event Fabric composition in -short mode")
	}
	cfg, err := config.Load(writeConfig(t, t.TempDir()))
	require.NoError(t, err)
	descriptor := deployStandby(descriptorOnFreePorts(t, cfg))

	active, err := openSite(t, descriptor, cfg, true, redundancy.RolePrimary)
	require.NoError(t, err)
	t.Cleanup(func() { _ = active.close(context.Background()) })
	require.True(t, active.ready, "the active process announces readiness")
	require.True(t, active.fabric.Info().HostsStorage, "the active process stores the journal")

	standby, err := openSite(t, descriptor, cfg, false, redundancy.RoleStandby)
	require.NoError(t, err)
	t.Cleanup(func() { _ = standby.close(context.Background()) })

	// On a lone machine the site has two instances, which is below the three a
	// replicated journal needs, so one is selected for storage and the standby is
	// a client of it. A standby that is itself a storage member is the four
	// instance case, which needs two machines and is a scenario.
	require.False(t, standby.fabric.Info().HostsStorage,
		"a site of two instances runs one storage node")

	// It holds no active capability: no handler, no command or query service, and
	// it never announces readiness.
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
	err = active.publisher.Publish(t.Context(), eventfabric.Ready{Info: active.fabric.Info()})
	require.NoError(t, err)
	high, err := active.fabric.HighWater(t.Context())
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		st, stateErr := standby.fabric.State(t.Context())
		return stateErr == nil && st.Applied >= high
	}, 10*time.Second, 20*time.Millisecond, "the standby did not follow a new journal event")
}

func TestRunReportsMissingConfigFlag(t *testing.T) {
	require.Error(t, Run([]string{"-unknown"}))
}

func TestRunReportsUnusableConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte("[invalid"), 0o644))
	require.ErrorContains(t, Run([]string{"-config", path}), "invalid configuration file")
}

// TestOpenReportsUnusableJournalStorage checks a node fails at startup rather
// than when its first event needs writing. The journal is the site's history: a
// platform that cannot store it must not start and pretend otherwise.
//
// It sabotages the descriptor's JetStream store directory rather than the
// configuration file, because the path is authored per instance in the
// blueprint. The end-to-end form of this, sabotaging what the blueprint authored
// and starting the built binary, is the scenario suite's
// resilience.PlatformRefusesToStartWithoutItsJournalStorage.
func TestOpenReportsUnusableJournalStorage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Event Fabric composition in -short mode")
	}
	dir := t.TempDir()
	cfg, err := config.Load(writeConfig(t, dir))
	require.NoError(t, err)

	// A file where the directory has to be, so creating it cannot succeed. It
	// stands in for the real cases: no permission, or a full or unmounted disk.
	blocked := filepath.Join(dir, "not-a-dir")
	require.NoError(t, os.WriteFile(blocked, []byte("x"), 0o644))
	descriptor := descriptorOnFreePorts(t, cfg)
	descriptor.Instances.Primary.Nats.JetStreamStoreDir = blocked

	_, err = openSite(t, descriptor, cfg, true, redundancy.RolePrimary)
	require.ErrorContains(t, err, "JetStream store directory")
	require.ErrorContains(t, err, "nats:", "the failure names the storage it could not use")
}

// TestOpenReportsUnusableJsonlDataDir checks a process fails at startup when the
// local record's data directory cannot be created. The record is mandatory and
// is opened before anything else, because every fact this process states has to
// reach it, including the ones about failing to start.
func TestOpenReportsUnusableJsonlDataDir(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Load(writeConfig(t, dir))
	require.NoError(t, err)

	blocked := filepath.Join(dir, "not-a-dir")
	require.NoError(t, os.WriteFile(blocked, []byte("x"), 0o644))
	descriptor := descriptorOnFreePorts(t, cfg)
	// Sabotage the DataDir: the JSONL backend will try to create a subdirectory
	// under it, which will fail because blocked is a file, not a directory.
	descriptor.Instances.Primary.DataDir = blocked

	_, err = newTestProcess(t, descriptor, cfg, redundancy.RolePrimary)
	require.ErrorContains(t, err, "jsonl:", "the failure identifies the JSONL backend")
}

// TestPublishedEventsReachBothJsonlAndNats verifies that the fan-out pipeline
// writes each envelope to the JSONL file and to the NATS journal. Runtime
// composition is the only place that knows the backend list; no producer
// chooses backends.
func TestPublishedEventsReachBothJsonlAndNats(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Event Fabric composition in -short mode")
	}
	cfg, err := config.Load(writeConfig(t, t.TempDir()))
	require.NoError(t, err)
	desc := descriptorOnFreePorts(t, cfg)
	jsonlPath := filepath.Join(desc.Instances.Primary.DataDir, "events", "events.jsonl")

	s, err := openSite(t, desc, cfg, true, redundancy.RolePrimary)
	require.NoError(t, err)

	// Publish one extra event so the JSONL file contains at least our line.
	err = s.publisher.Publish(t.Context(), SiteOpening{Active: true})
	require.NoError(t, err)

	// Releasing the site closes the transport. The local record is the process's
	// and stays open; every Store synced it, so what it holds is already on disk.
	require.NoError(t, s.close(t.Context()))

	// The JSONL file must exist and contain valid JSON envelopes.
	f, err := os.Open(jsonlPath)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	require.NoError(t, scanner.Err())
	require.NotEmpty(t, lines, "JSONL file must contain at least one event line")
	for _, line := range lines {
		require.Contains(t, line, `"id"`, "every JSONL line must be a JSON envelope")
	}

	// The NATS journal also received the events: startup published SiteOpening,
	// SiteReady, etc., and the projector replayed them into the projection.
	// The site reached s.ready == true, so the journal is non-empty.
	require.True(t, s.ready, "site reached readiness, confirming events reached NATS too")
}

// TestPrimaryAndStandbyWriteSeparateJsonlFiles verifies that each instance
// writes to its own DataDir, so they never share a JSONL file.
func TestPrimaryAndStandbyWriteSeparateJsonlFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping redundant Event Fabric composition in -short mode")
	}
	cfg, err := config.Load(writeConfig(t, t.TempDir()))
	require.NoError(t, err)
	descriptor := deployStandby(descriptorOnFreePorts(t, cfg))

	require.NotEqual(t,
		descriptor.Instances.Primary.DataDir,
		descriptor.Instances.Standby.DataDir,
		"descriptor gives each instance its own DataDir")

	primaryJsonl := filepath.Join(descriptor.Instances.Primary.DataDir, "events", "events.jsonl")
	standbyJsonl := filepath.Join(descriptor.Instances.Standby.DataDir, "events", "events.jsonl")
	require.NotEqual(t, primaryJsonl, standbyJsonl,
		"primary and standby must write to separate JSONL files")

	active, err := openSite(t, descriptor, cfg, true, redundancy.RolePrimary)
	require.NoError(t, err)
	t.Cleanup(func() { _ = active.close(context.Background()) })

	standby, err := openSite(t, descriptor, cfg, false, redundancy.RoleStandby)
	require.NoError(t, err)
	t.Cleanup(func() { _ = standby.close(context.Background()) })

	// A standby never calls publisher.Publish during startup (no readiness, no
	// handlers), so publish one explicit event to exercise its JSONL pipeline.
	require.NoError(t, standby.publisher.Publish(t.Context(), SiteOpening{Active: false}))

	// Release both. Each instance composed its own process record, so the two
	// files were never one file at any point.
	require.NoError(t, active.close(t.Context()))
	require.NoError(t, standby.close(t.Context()))

	// Primary JSONL: non-empty because startup publishes a Ready event.
	pInfo, err := os.Stat(primaryJsonl)
	require.NoError(t, err)
	require.Positive(t, pInfo.Size(), "primary JSONL file must contain events")

	// Standby JSONL: exists and non-empty from the event we published above.
	sInfo, err := os.Stat(standbyJsonl)
	require.NoError(t, err)
	require.Positive(t, sInfo.Size(), "standby JSONL file must contain the event published to it")
}

// failingRecord is a storage.Backend that refuses everything, which is what an
// unwritable local record looks like to a producer.
type failingRecord struct{ err error }

func (b failingRecord) Store(context.Context, events.Envelope) error { return b.err }

func (b failingRecord) Close(context.Context) error { return nil }

// TestSiteOpenReturnsAFailureToStateThatItIsOpening checks the error policy on a
// startup path: a composition that cannot write its local record does not
// quietly carry on composing.
func TestSiteOpenReturnsAFailureToStateThatItIsOpening(t *testing.T) {
	recordUnwritable := errors.New("jsonl: write events.jsonl: disk is full")
	cfg, err := config.Load(writeConfig(t, t.TempDir()))
	require.NoError(t, err)
	descriptor := descriptorOnFreePorts(t, cfg)

	proc, err := newTestProcess(t, descriptor, cfg, redundancy.RolePrimary)
	require.NoError(t, err)
	local, err := storage.NewPublisher(proc.factory, failingRecord{err: recordUnwritable})
	require.NoError(t, err)
	proc.local = local

	_, err = open(t.Context(), proc, true)

	require.ErrorIs(t, err, recordUnwritable,
		"a site that cannot state that it is opening reports it rather than opening anyway")
}

// TestTheSiteStatesItsOwnCompositionLocallyAndTheSitesFactsToTheJournal pins
// which publisher carries what. Composition facts describe one process and go to
// the local record only; the facts the site is supposed to hear go to both
// backends. Nothing decides this but runtime composition.
func TestTheSiteStatesItsOwnCompositionLocallyAndTheSitesFactsToTheJournal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Event Fabric composition in -short mode")
	}
	cfg, err := config.Load(writeConfig(t, t.TempDir()))
	require.NoError(t, err)
	s, err := openSite(t, descriptorOnFreePorts(t, cfg), cfg, true, redundancy.RolePrimary)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.close(context.Background()) })

	high, err := s.fabric.HighWater(t.Context())
	require.NoError(t, err)

	require.NoError(t, s.local.Publish(t.Context(), SiteOpening{Active: true}))
	afterLocal, err := s.fabric.HighWater(t.Context())
	require.NoError(t, err)
	require.Equal(t, high, afterLocal, "a fact about this process alone never reaches the journal")

	require.NoError(t, s.publisher.Publish(t.Context(), eventfabric.Stopping{Adapter: natsbackend.Name}))
	afterJournal, err := s.fabric.HighWater(t.Context())
	require.NoError(t, err)
	require.Equal(t, high+1, afterJournal, "a fact the site is supposed to hear reaches the journal")
}
