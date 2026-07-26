package harness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOperationEventsReadsPrimaryAndStandbyRecords(t *testing.T) {
	primary := filepath.Join(t.TempDir(), "primary", "events.jsonl")
	standby := filepath.Join(t.TempDir(), "standby", "events.jsonl")
	writeScenarioEvents(t, primary, `{"type":"primary"}`)
	writeScenarioEvents(t, standby, `{"type":"standby"}`)

	machine := &Machine{Sockets: Sockets{EventsFile: primary, StandbyEventsFile: standby}}
	events := operationEvents(machine)

	require.Contains(t, events, `{"type":"primary"}`)
	require.Contains(t, events, `{"type":"standby"}`)
}

func writeScenarioEvents(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content+"\n"), 0o644))
}

func TestInstanceEpochReadsTheStateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(path, []byte(
		`{"epoch":7,"updated_at":"2026-07-26T09:15:42Z",`+
			`"process_epoch":{"count":3,"updated_at":"2026-07-26T09:15:42Z"},`+
			`"activation_epoch":{"count":4,"updated_at":"2026-07-26T08:04:11Z"}}`), 0o644))

	require.Equal(t, InstanceEpochs{Epoch: 7, Process: 3, Activation: 4}, InstanceEpoch(t, path))
}
