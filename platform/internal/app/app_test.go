package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage/jsonl"
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

// uniqueLockMutex returns an ownership mutex name no other test or run shares.
func uniqueLockMutex(t *testing.T) string {
	t.Helper()
	token := make([]byte, 8)
	_, err := rand.Read(token)
	require.NoError(t, err)
	return `Global\opdl-app-test.` + hex.EncodeToString(token)
}

// newTestProcess composes what Run composes before it opens anything: one
// envelope factory for the process, the mandatory local record, and the
// process-local publisher over it.
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
	state fabricState
	err   error
}

func (f fixedStatusFabric) State(context.Context) (fabricState, error) {
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
		fixedStatusFabric{state: fabricState{CaughtUp: true}},
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
	fabric := &togglingFabric{state: fabricState{CaughtUp: true, Applied: 7, HighWater: 7}}

	done, err := startFailoverMonitor(ctx, publisher, fabric, redundancy.StatePassive, time.Nanosecond, nil)
	require.NoError(t, err)

	// A projection that falls behind for longer than the bound is no longer a
	// machine anyone can be handed.
	fabric.set(fabricState{CaughtUp: false, Applied: 7, HighWater: 9})
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
	state fabricState
}

func (f *togglingFabric) set(state fabricState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = state
}

func (f *togglingFabric) State(context.Context) (fabricState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, nil
}

func TestRunReportsMissingConfigFlag(t *testing.T) {
	require.Error(t, Run([]string{"-unknown"}))
}

func TestRunReportsUnusableConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte("[invalid"), 0o644))
	require.ErrorContains(t, Run([]string{"-config", path}), "invalid configuration file")
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

// TestSiteOpenReturnsAFailureToStateThatItIsOpening checks the error policy on a
// startup path: a composition that cannot write its local record does not
// quietly carry on composing.
func TestSiteOpenReturnsAFailureToStateThatItIsOpening(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, t.TempDir()))
	require.NoError(t, err)
	descriptor := descriptorOnFreePorts(t, cfg)

	proc, err := newTestProcess(t, descriptor, cfg, redundancy.RolePrimary)
	require.NoError(t, err)

	_, err = open(t.Context(), proc, true)
	require.ErrorIs(t, err, api.ErrNotImplemented)
}
