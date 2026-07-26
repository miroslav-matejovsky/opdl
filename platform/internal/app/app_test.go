package app

import (
	"context"
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
	"github.com/miroslav-matejovsky/opdl/platform/internal/instancestate"
	"github.com/miroslav-matejovsky/opdl/platform/internal/redundancy"
	"github.com/miroslav-matejovsky/opdl/platform/internal/registration"
	"github.com/miroslav-matejovsky/opdl/utils/testnet"
)

// This file covers what runtime composition owns: deriving the Event Fabric's
// configuration from the deployment, placing a node's storage, reading the
// trusted topology, and the startup and shutdown ordering.
//
// The composition tests open real sockets, so they are integration tests and stay
// out of the fast gate. Everything derivable without a socket is tested without
// one.

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

// loadConfig loads the configuration compiled into the test binary: the neutral
// mock descriptor, which is the only configuration a process has.
//
// It names the deployment's real ports and paths, which several tests running at
// once cannot all take, so a test that needs its own moves the descriptor
// through descriptorOnFreePorts. That keeps the contract intact: the descriptor
// is still the single source of the machine's topology, and each instance still
// reads only its own record.
func loadConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load()
	require.NoError(t, err)
	return cfg
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
// Both instance records are filled in, whether or not a test exercises the
// standby. A test that needs one then has it already, rather than composing a
// second endpoint set by hand, which is how a standby ends up on the primary's
// address.
func descriptorOnFreePorts(t *testing.T, cfg *config.Config) config.Descriptor {
	t.Helper()
	descriptor := cfg.Descriptor()
	dataRoot := t.TempDir()
	onFreePort := func(role config.PlatformInstanceRole) config.Instance {
		instance := descriptor.Instance(role)
		instance.APIAddress = freeAddress(t)
		instance.EventsFile = filepath.Join(dataRoot, string(role), "events.jsonl")
		instance.StateFile = filepath.Join(dataRoot, string(role), "state.json")
		return instance
	}
	descriptor.Primary = onFreePort(config.RolePrimary)
	standby := onFreePort(config.RoleStandby)
	descriptor.Standby = &standby
	descriptor.Lease = &config.Lease{
		File:                  filepath.Join(dataRoot, "lease"),
		Duration:              "15s",
		RenewalInterval:       "5s",
		HealthCheckInterval:   "2s",
		FailbackStabilization: "30s",
	}
	return descriptor
}

// newTestProcess composes what Run composes before it opens anything: one
// envelope factory for the process, the mandatory local record, and the
// process-local publisher over it.
func newTestProcess(t *testing.T, descriptor config.Descriptor, cfg *config.Config, role redundancy.InstanceRole) (process, error) {
	t.Helper()
	factory, err := events.NewFactory(descriptor, role.String())
	require.NoError(t, err)
	record, err := jsonl.New(instanceOf(descriptor, role).EventsFile)
	if err != nil {
		return process{}, err
	}
	t.Cleanup(func() { _ = record.Close(context.Background()) })
	local, err := storage.NewPublisher(factory, record)
	require.NoError(t, err)
	state, err := instancestate.Open(instanceOf(descriptor, role).StateFile)
	require.NoError(t, err)
	return process{
		descriptor: descriptor,
		cfg:        cfg,
		role:       role,
		factory:    factory,
		local:      local,
		record:     record,
		state:      state,
	}, nil
}

// TestTopologyExpectsEverySiteMachineIncludingItself checks the trusted
// registration topology is the descriptor's static membership. Acceptance needs
// every expected machine, so the set must never be "who is reachable".
func TestTopologyExpectsEverySiteMachineIncludingItself(t *testing.T) {
	self, expected := topology(config.Descriptor{
		Site: "north", Machine: "node-a", IP: "10.0.1.10",
	})
	require.Equal(t, registration.Location{Machine: "node-a", IP: "10.0.1.10"}, self)
	require.Equal(t, []registration.Location{
		{Machine: "node-a", IP: "10.0.1.10"},
	}, expected)

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

func TestRunRejectsAnUnknownFlag(t *testing.T) {
	require.Error(t, Run([]string{"-unknown"}))
}

// TestRunRequiresAnInstanceRole checks the one thing a launch still decides is
// required. Everything else a process runs with is compiled into it.
func TestRunRequiresAnInstanceRole(t *testing.T) {
	require.ErrorContains(t, Run(nil), "-instance primary|standby is required")
}

// TestOpenReportsUnusableEventsFile checks a process fails at startup when the
// local record cannot be opened. The record is mandatory and is opened before
// anything else, because every fact this process states has to reach it,
// including the ones about failing to start.
func TestOpenReportsUnusableEventsFile(t *testing.T) {
	dir := t.TempDir()
	cfg := loadConfig(t)

	blocked := filepath.Join(dir, "not-a-dir")
	require.NoError(t, os.WriteFile(blocked, []byte("x"), 0o644))
	descriptor := descriptorOnFreePorts(t, cfg)
	// Sabotage the events file: the JSONL backend will try to create its parent
	// directory, which will fail because blocked is a file, not a directory.
	descriptor.Primary.EventsFile = filepath.Join(blocked, "events.jsonl")

	_, err := newTestProcess(t, descriptor, cfg, redundancy.RolePrimary)
	require.ErrorContains(t, err, "jsonl:", "the failure identifies the JSONL backend")
}

// TestSiteOpenReturnsAFailureToStateThatItIsOpening checks the error policy on a
// startup path: a composition that cannot write its local record does not
// quietly carry on composing.
func TestSiteOpenReturnsAFailureToStateThatItIsOpening(t *testing.T) {
	cfg := loadConfig(t)
	descriptor := descriptorOnFreePorts(t, cfg)

	proc, err := newTestProcess(t, descriptor, cfg, redundancy.RolePrimary)
	require.NoError(t, err)

	_, err = open(t.Context(), proc, true)
	require.ErrorIs(t, err, api.ErrNotImplemented)
}
