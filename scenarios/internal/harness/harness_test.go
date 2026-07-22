package harness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlatformConfigContainsOnlyRuntimeSettings(t *testing.T) {
	config := string(platformConfig())

	require.Contains(t, config, "[event_fabric.nats]")
	require.NotContains(t, config, "[operations]")
	require.NotContains(t, config, "event_dir")
	require.NotContains(t, config, "data_dir")
}

func TestOperationEventsReadsPrimaryAndStandbyRecords(t *testing.T) {
	primary := filepath.Join(t.TempDir(), "primary")
	standby := filepath.Join(t.TempDir(), "standby")
	writeScenarioEvents(t, primary, `{"type":"primary"}`)
	writeScenarioEvents(t, standby, `{"type":"standby"}`)

	machine := &Machine{Sockets: Sockets{DataDir: primary, StandbyDataDir: standby}}
	events := operationEvents(machine)

	require.Contains(t, events, `{"type":"primary"}`)
	require.Contains(t, events, `{"type":"standby"}`)
}

func writeScenarioEvents(t *testing.T, dataDir, content string) {
	t.Helper()
	path := filepath.Join(dataDir, "events", "events.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content+"\n"), 0o644))
}
