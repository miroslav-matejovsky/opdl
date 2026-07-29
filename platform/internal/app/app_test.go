package app

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage"
	"github.com/miroslav-matejovsky/opdl/platform/internal/instance/eventlog"
	"github.com/miroslav-matejovsky/opdl/platform/internal/instance/state"
	"github.com/miroslav-matejovsky/opdl/platform/internal/machine/redundancy"
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
		instance.LogFile = filepath.Join(dataRoot, string(role), "platform.log")
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
	record, err := eventlog.New(instanceOf(descriptor, role).EventsFile)
	if err != nil {
		return process{}, err
	}
	t.Cleanup(func() { _ = record.Close(context.Background()) })
	local, err := storage.NewPublisher(factory, record)
	require.NoError(t, err)
	st, err := state.Open(instanceOf(descriptor, role).StateFile)
	require.NoError(t, err)
	return process{
		descriptor: descriptor,
		cfg:        cfg,
		role:       role,
		local:      local,
		state:      st,
		// The application log is the process's, opened by Run from the descriptor.
		// These tests compose the parts below it, so they discard what it would
		// have written rather than opening a file nothing reads.
		log: slog.New(slog.DiscardHandler),
	}, nil
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
