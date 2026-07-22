package operations

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// testDescriptor is the deployment identity the recorder's factory stamps.
var testDescriptor = config.Descriptor{
	Platform: "opdl", Project: "p", Environment: "production",
	Site: "west", Machine: "node-a", MachineProfile: "all-in-one",
}

// openRecorder opens a recorder over a fresh directory, writing its error
// stream to a buffer the test can read.
func openRecorder(t *testing.T, role string) (*Recorder, *bytes.Buffer) {
	t.Helper()
	factory, err := events.NewFactory(testDescriptor, role)
	require.NoError(t, err)
	recorder, err := Open(t.TempDir(), factory)
	require.NoError(t, err)
	log := &bytes.Buffer{}
	recorder.stderr = log
	return recorder, log
}

// probe is a typed event this package's tests state. The recorder owns the
// mechanism and no events of its own, so its tests bring their own.
type probe struct {
	Detail string `json:"detail"`
}

func (probe) EventType() events.Type { return "platform.test.happened" }

// failedProbe declares error severity, so a recorder test can prove the
// envelope carries what the payload declared.
type failedProbe struct {
	Error string `json:"error"`
}

func (failedProbe) EventType() events.Type    { return "platform.test.failed" }
func (failedProbe) Severity() events.Severity { return events.SeverityError }

func TestRecordWritesTheSameCanonicalEnvelopeToBothSinks(t *testing.T) {
	recorder, log := openRecorder(t, "primary")
	recorder.Record(t.Context(), probe{Detail: "started"})
	require.NoError(t, recorder.Close())

	fileData, err := os.ReadFile(recorder.Path())
	require.NoError(t, err)
	require.Equal(t, log.String(), string(fileData), "an operator reading either sink reads the same event")

	envelope, err := events.Decode(bytes.TrimSpace(fileData))
	require.NoError(t, err)
	require.NoError(t, envelope.Validate())
	require.Equal(t, events.Type("platform.test.happened"), envelope.Type)
	require.Equal(t, "test", envelope.Source)
	require.Equal(t, events.SeverityInfo, envelope.Severity)
	require.Equal(t, "node-a", envelope.Origin.Machine)
	require.Equal(t, "primary", envelope.Origin.ProcessRole)
	require.Positive(t, envelope.Origin.PID)
	require.JSONEq(t, `{"detail":"started"}`, string(envelope.Data))
}

func TestRecordKeepsTheSeverityAnEventDeclares(t *testing.T) {
	recorder, log := openRecorder(t, "primary")
	recorder.Record(t.Context(), failedProbe{Error: "disk full"})
	require.NoError(t, recorder.Close())

	envelope, err := events.Decode(bytes.TrimSpace(log.Bytes()))
	require.NoError(t, err)
	require.Equal(t, events.SeverityError, envelope.Severity)
	require.JSONEq(t, `{"error":"disk full"}`, string(envelope.Data),
		"a failure keeps the detail an operator needs")
}

func TestRecordReportsAnUnstampableEventWithoutFailing(t *testing.T) {
	recorder, log := openRecorder(t, "primary")
	recorder.Record(t.Context(), unroutableProbe{})
	require.NoError(t, recorder.Close())

	require.Contains(t, log.String(), "stamping failed",
		"a process that cannot describe itself still runs, and says so")
	fileData, err := os.ReadFile(recorder.Path())
	require.NoError(t, err)
	require.Empty(t, fileData, "nothing unreadable is retained")
}

// unroutableProbe declares a type that is not platform.<source>.<fact>.
type unroutableProbe struct{}

func (unroutableProbe) EventType() events.Type { return "test.unroutable" }

func TestRecordWritesOneJSONObjectPerLine(t *testing.T) {
	recorder, _ := openRecorder(t, "standby")
	recorder.Record(t.Context(), probe{Detail: "first"})
	recorder.Record(t.Context(), probe{Detail: "second"})
	require.NoError(t, recorder.Close())

	file, err := os.Open(recorder.Path())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, file.Close()) })
	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		_, err := events.Decode(scanner.Bytes())
		require.NoError(t, err)
		count++
	}
	require.NoError(t, scanner.Err())
	require.Equal(t, 2, count)
}

func TestPathNamesTheWritingProcess(t *testing.T) {
	recorder, _ := openRecorder(t, "standby")
	t.Cleanup(func() { require.NoError(t, recorder.Close()) })

	require.Contains(t, recorder.Path(), "node-a")
	require.Contains(t, recorder.Path(), "standby", "two instances of one machine write to two files")
}

// These cover the temporary Emit wrapper and go with it.

func TestEmitWritesSameStructuredEventToLogAndJSONL(t *testing.T) {
	recorder, log := openRecorder(t, "primary")
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
	require.Len(t, event.Attributes, 1)
}
