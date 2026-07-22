package operations

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// testDescriptor is the deployment identity the recorder's factory stamps.
var testDescriptor = config.Descriptor{
	Platform: "opdl", Project: "p", Environment: "production",
	Site: "west", Machine: "node-a", MachineProfile: "all-in-one",
}

// mustFactory composes the process identity a test recorder stamps with.
func mustFactory(t *testing.T, role string) events.Factory {
	t.Helper()
	factory, err := events.NewFactory(testDescriptor, role)
	require.NoError(t, err)
	return factory
}

// openRecorder opens a recorder over a fresh directory, writing its error
// stream to a buffer the test can read.
func openRecorder(t *testing.T, role string) (*Recorder, *bytes.Buffer) {
	t.Helper()
	recorder, err := Open(t.TempDir(), mustFactory(t, role))
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

func TestRecordIsSafeForConcurrentCallbacks(t *testing.T) {
	const writers, each = 8, 25
	recorder, log := openRecorder(t, "primary")

	var running sync.WaitGroup
	running.Add(writers)
	for range writers {
		go func() {
			defer running.Done()
			for range each {
				recorder.Record(t.Context(), probe{Detail: "concurrent"})
			}
		}()
	}
	running.Wait()
	require.NoError(t, recorder.Close())

	// Interleaved writes must not tear: every line is still one whole envelope,
	// and both sinks received the same bytes.
	fileData, err := os.ReadFile(recorder.Path())
	require.NoError(t, err)
	require.Equal(t, log.String(), string(fileData))
	lines := strings.Split(strings.TrimSpace(string(fileData)), "\n")
	require.Len(t, lines, writers*each)
	for _, line := range lines {
		envelope, err := events.Decode([]byte(line))
		require.NoError(t, err)
		require.NoError(t, envelope.Validate())
	}
}

func TestRecordReportsABrokenSinkToTheOtherOne(t *testing.T) {
	recorder, _ := openRecorder(t, "primary")
	recorder.stderr = brokenWriter{}
	recorder.Record(t.Context(), probe{Detail: "started"})
	require.NoError(t, recorder.Close())

	fileData, err := os.ReadFile(recorder.Path())
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(fileData)), "\n")
	require.Len(t, lines, 2)
	require.Contains(t, lines[0], "stderr write failed",
		"the terminal fallback reports the broken sink as plain text, not as an event")
	envelope, err := events.Decode([]byte(lines[1]))
	require.NoError(t, err)
	require.Equal(t, events.Type("platform.test.happened"), envelope.Type,
		"a broken sink does not stop the working one")
}

// brokenWriter stands in for a sink that has failed, such as a closed pipe to a
// service manager.
type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("sink is gone") }

func TestCloseIsIdempotentAndReportsItsErrors(t *testing.T) {
	recorder, _ := openRecorder(t, "primary")
	recorder.Record(t.Context(), probe{Detail: "started"})

	require.NoError(t, recorder.Close())
	require.NoError(t, recorder.Close(), "closing twice reports the same answer")

	// A recorder with no file has nothing to close, and neither has a nil one:
	// the open failure path and the discard recorder both rely on that.
	withoutFile, err := Open("", mustFactory(t, "primary"))
	require.NoError(t, err)
	require.Empty(t, withoutFile.Path())
	require.NoError(t, withoutFile.Close())
	require.NoError(t, (*Recorder)(nil).Close())
}

func TestCloseReportsASyncFailure(t *testing.T) {
	recorder, _ := openRecorder(t, "primary")
	recorder.Record(t.Context(), probe{Detail: "started"})
	// Closing the handle underneath leaves the recorder holding a file it can
	// neither sync nor close, which is what an unmounted volume looks like.
	require.NoError(t, recorder.file.Close())

	err := recorder.Close()
	require.ErrorContains(t, err, "sync operations event file")
	require.ErrorContains(t, err, "close operations event file")
	require.ErrorContains(t, err, recorder.Path(), "the failure names the file it was writing")
}
