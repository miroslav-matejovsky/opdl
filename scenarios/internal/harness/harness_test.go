package harness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlatformConfigContainsOnlyRuntimeSettings(t *testing.T) {
	config := string(platformConfig(true))

	require.Contains(t, config, "lag_bound")
	require.NotContains(t, config, "[operations]")
	require.NotContains(t, config, "event_dir")
	require.NotContains(t, config, "data_dir")
}

// TestPlatformConfigOmitsJournalBoundsWithoutEventStorage checks the harness
// writes a machine only the settings its deployment reads.
//
// The one it drops bounds a site journal. A machine with no event storage
// has none, so a file stating it would be describing something that does not
// exist, and the scenario would stop being evidence that the runtime does not
// require it.
func TestPlatformConfigOmitsJournalBoundsWithoutEventStorage(t *testing.T) {
	config := string(platformConfig(false))

	require.Contains(t, config, "read_header_timeout")
	require.Contains(t, config, "shutdown_timeout")
	require.NotContains(t, config, "lag_bound")
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
