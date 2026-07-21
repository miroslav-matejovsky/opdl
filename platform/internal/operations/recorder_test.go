package operations

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/stretchr/testify/require"
)

func TestRecorderWritesSameStructuredEventToLogAndJSONL(t *testing.T) {
	recorder, err := Open(t.TempDir(), deployment.Descriptor{
		Project: "p", Environment: "production", Site: "west", Machine: "node-a",
	}, "primary")
	require.NoError(t, err)
	var log bytes.Buffer
	recorder.stderr = &log
	recorder.now = func() time.Time { return time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC) }
	recorder.Emit("event_fabric.connected", LevelInfo, "event_fabric.nats", "connected", map[string]any{"server": "nats://127.0.0.1:4222"})
	require.NoError(t, recorder.Close())

	fileData, err := os.ReadFile(recorder.Path())
	require.NoError(t, err)
	require.Equal(t, log.String(), string(fileData))
	var event Event
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(fileData), &event))
	require.Equal(t, "event_fabric.connected", event.Type)
	require.Equal(t, "node-a", event.Machine)
	require.Equal(t, float64(1), float64(len(event.Attributes)))
}

func TestRecorderEmitsOneJSONObjectPerLine(t *testing.T) {
	recorder, err := Open(t.TempDir(), deployment.Descriptor{Machine: "node-a"}, "standby")
	require.NoError(t, err)
	recorder.stderr = &bytes.Buffer{}
	recorder.Emit("one", LevelInfo, "test", "first", nil)
	recorder.Emit("two", LevelWarn, "test", "second", nil)
	require.NoError(t, recorder.Close())

	file, err := os.Open(recorder.Path())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, file.Close()) })
	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		var event Event
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &event))
		count++
	}
	require.NoError(t, scanner.Err())
	require.Equal(t, 2, count)
}
