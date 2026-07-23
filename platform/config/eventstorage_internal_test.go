package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These tests are in the package rather than beside the rest of the
// configuration tests because Load always reads the descriptor compiled into the
// binary, which has event storage. What is required of the file depends on the
// descriptor, so covering the other half means composing a descriptor here and
// calling the decision directly.

// journalless is the descriptor of a machine whose blueprint authored no event
// storage: one deployed instance, with no nats record.
func journalless() Descriptor {
	return Descriptor{
		Instances: Instances{
			Primary: Instance{DataDir: ".data/platform/primary", APIAddress: "127.0.0.1:8080"},
			Standby: Instance{Disabled: true},
		},
	}
}

// journalled is the same machine with event storage on its Primary Instance.
func journalled() Descriptor {
	d := journalless()
	d.Instances.Primary.Nats = &Nats{JetStreamStoreDir: ".data/journal/primary"}
	return d
}

func TestHasEventStorageIgnoresAnInstanceThatIsNotDeployed(t *testing.T) {
	require.False(t, journalless().HasEventStorage())
	require.True(t, journalled().HasEventStorage())

	// A standby that is not deployed carries nothing, so a machine whose only
	// deployed instance has no journal has none, whatever the standby record says.
	d := journalless()
	d.Instances.Standby = Instance{Disabled: true, Nats: &Nats{JetStreamStoreDir: "leftover"}}
	require.False(t, d.HasEventStorage(), "an instance that does not run cannot give the machine a journal")
}

// TestEventStorageSettingsAreRequiredOnlyWithAJournal is the rule the
// configuration file follows: the three settings that bound a site journal are
// required of a deployment that has one and of no other.
func TestEventStorageSettingsAreRequiredOnlyWithAJournal(t *testing.T) {
	empty := file{}

	lagBound, err := eventStorageSettings("config.toml", journalless(), empty)
	require.NoError(t, err, "a deployment with no journal needs no bounds for one")
	require.Zero(t, lagBound, "and has no lag bound, because it has no projection")

	_, err = eventStorageSettings("config.toml", journalled(), empty)
	require.ErrorContains(t, err, "lag_bound is required")
}

// TestEventStorageSettingsValidateAStatedValueThatDoesNotApply keeps a file
// copied from a deployment that had a journal honest.
//
// Dropping the requirement is not the same as ignoring the setting. A value that
// is stated is parsed whether this deployment reads it or not, so a typo is a
// startup failure rather than a line that looks like configuration and is not.
func TestEventStorageSettingsValidateAStatedValueThatDoesNotApply(t *testing.T) {
	stated := file{LagBound: "not-a-duration"}
	_, err := eventStorageSettings("config.toml", journalless(), stated)
	require.ErrorContains(t, err, "invalid lag_bound")

	stated = file{EventFabric: EventFabric{Nats: EventFabricNats{StartupTimeout: "-5s"}}}
	_, err = eventStorageSettings("config.toml", journalless(), stated)
	require.ErrorContains(t, err, "duration must be positive")

	// A well-formed value that does not apply is accepted and left unused.
	stated = file{LagBound: "30s", EventFabric: EventFabric{Nats: EventFabricNats{StartupTimeout: "30s", CatchUpTimeout: "25s"}}}
	lagBound, err := eventStorageSettings("config.toml", journalless(), stated)
	require.NoError(t, err)
	require.Zero(t, lagBound, "a bound this deployment does not read is not adopted")
}

// TestEventStorageSettingsRequireEachSettingWithAJournal checks the three are
// required individually rather than as a block, so a partly filled file names
// the setting that is missing.
func TestEventStorageSettingsRequireEachSettingWithAJournal(t *testing.T) {
	tests := []struct {
		name    string
		file    file
		errText string
	}{
		{
			"no startup timeout",
			file{LagBound: "30s", EventFabric: EventFabric{Nats: EventFabricNats{CatchUpTimeout: "25s"}}},
			"[event_fabric.nats] startup_timeout is required",
		},
		{
			"no catch-up timeout",
			file{LagBound: "30s", EventFabric: EventFabric{Nats: EventFabricNats{StartupTimeout: "30s"}}},
			"[event_fabric.nats] catch_up_timeout is required",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := eventStorageSettings("config.toml", journalled(), tc.file)
			require.ErrorContains(t, err, tc.errText)
		})
	}

	complete := file{LagBound: "30s", EventFabric: EventFabric{Nats: EventFabricNats{StartupTimeout: "30s", CatchUpTimeout: "25s"}}}
	lagBound, err := eventStorageSettings("config.toml", journalled(), complete)
	require.NoError(t, err)
	require.Equal(t, 30*time.Second, lagBound)
}
