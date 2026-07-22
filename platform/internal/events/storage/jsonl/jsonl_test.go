package jsonl_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage/jsonl"
)

var testDescriptor = config.Descriptor{
	Platform:       "opdl",
	Project:        "scenario",
	Environment:    "development",
	Site:           "local",
	Machine:        "node",
	MachineProfile: "all-in-one",
	IP:             "127.0.0.1",
	Services:       []string{"core-services"},
}

func testEnvelope(t *testing.T, id string) events.Envelope {
	t.Helper()
	factory, err := events.NewFactory(testDescriptor, "primary")
	require.NoError(t, err)

	env, err := factory.Wrap(t.Context(), sampleEvent{ID: id})
	require.NoError(t, err)
	return env
}

type sampleEvent struct {
	ID string `json:"id"`
}

func (sampleEvent) EventType() events.Type { return "platform.test.happened" }

func TestNewValidation(t *testing.T) {
	_, err := jsonl.New("   ")
	require.ErrorIs(t, err, jsonl.ErrInvalidPath)
}

func TestOpeningCreatesOnlyExpectedSubdirectoryAndFile(t *testing.T) {
	dataDir := t.TempDir()

	backend, err := jsonl.New(dataDir)
	require.NoError(t, err)
	defer func() { _ = backend.Close(t.Context()) }()

	eventsDir := filepath.Join(dataDir, "events")
	eventsFile := filepath.Join(eventsDir, "events.jsonl")

	require.DirExists(t, eventsDir)
	require.FileExists(t, eventsFile)

	rootEntries, err := os.ReadDir(dataDir)
	require.NoError(t, err)
	require.Len(t, rootEntries, 1)
	require.Equal(t, "events", rootEntries[0].Name())

	eventsEntries, err := os.ReadDir(eventsDir)
	require.NoError(t, err)
	require.Len(t, eventsEntries, 1)
	require.Equal(t, "events.jsonl", eventsEntries[0].Name())
}

func TestOneStoredEnvelopeDecodesWithEventsDecode(t *testing.T) {
	dataDir := t.TempDir()
	backend, err := jsonl.New(dataDir)
	require.NoError(t, err)
	defer func() { _ = backend.Close(t.Context()) }()

	env := testEnvelope(t, "item-1")
	err = backend.Store(t.Context(), env)
	require.NoError(t, err)

	filePath := filepath.Join(dataDir, "events", "events.jsonl")
	content, err := os.ReadFile(filePath)
	require.NoError(t, err)

	lines := bytes.Split(bytes.TrimSuffix(content, []byte("\n")), []byte("\n"))
	require.Len(t, lines, 1)

	decoded, err := events.Decode(lines[0])
	require.NoError(t, err)
	require.Equal(t, env, decoded)
}

func TestSeveralEventsProduceLinesInCallOrder(t *testing.T) {
	dataDir := t.TempDir()
	backend, err := jsonl.New(dataDir)
	require.NoError(t, err)
	defer func() { _ = backend.Close(t.Context()) }()

	env1 := testEnvelope(t, "item-1")
	env2 := testEnvelope(t, "item-2")
	env3 := testEnvelope(t, "item-3")

	require.NoError(t, backend.Store(t.Context(), env1))
	require.NoError(t, backend.Store(t.Context(), env2))
	require.NoError(t, backend.Store(t.Context(), env3))

	content, err := os.ReadFile(filepath.Join(dataDir, "events", "events.jsonl"))
	require.NoError(t, err)

	lines := bytes.Split(bytes.TrimSuffix(content, []byte("\n")), []byte("\n"))
	require.Len(t, lines, 3)

	d1, err := events.Decode(lines[0])
	require.NoError(t, err)
	d2, err := events.Decode(lines[1])
	require.NoError(t, err)
	d3, err := events.Decode(lines[2])
	require.NoError(t, err)

	require.Equal(t, env1.ID, d1.ID)
	require.Equal(t, env2.ID, d2.ID)
	require.Equal(t, env3.ID, d3.ID)
}

func TestConcurrentStoresProduceValidNonInterleavedLines(t *testing.T) {
	dataDir := t.TempDir()
	backend, err := jsonl.New(dataDir)
	require.NoError(t, err)
	defer func() { _ = backend.Close(t.Context()) }()

	const count = 50
	var wg sync.WaitGroup
	wg.Add(count)

	for i := 0; i < count; i++ {
		go func(id int) {
			defer wg.Done()
			env := testEnvelope(t, fmt.Sprintf("concurrent-%d", id))
			err := backend.Store(t.Context(), env)
			require.NoError(t, err)
		}(i)
	}

	wg.Wait()

	content, err := os.ReadFile(filepath.Join(dataDir, "events", "events.jsonl"))
	require.NoError(t, err)

	lines := bytes.Split(bytes.TrimSuffix(content, []byte("\n")), []byte("\n"))
	require.Len(t, lines, count)

	seenIDs := make(map[string]bool, count)
	for _, line := range lines {
		decoded, err := events.Decode(line)
		require.NoError(t, err)
		require.False(t, seenIDs[decoded.ID], "duplicate or interleaved line: %s", decoded.ID)
		seenIDs[decoded.ID] = true
	}
	require.Len(t, seenIDs, count)
}

func TestInvalidEnvelopesAreRejectedBeforeLineIsWritten(t *testing.T) {
	dataDir := t.TempDir()
	backend, err := jsonl.New(dataDir)
	require.NoError(t, err)
	defer func() { _ = backend.Close(t.Context()) }()

	invalidEnv := events.Envelope{} // zero envelope is invalid
	err = backend.Store(t.Context(), invalidEnv)
	require.Error(t, err)
	require.ErrorIs(t, err, events.ErrInvalidEnvelope)

	filePath := filepath.Join(dataDir, "events", "events.jsonl")
	info, err := os.Stat(filePath)
	require.NoError(t, err)
	require.Equal(t, int64(0), info.Size(), "no byte written for invalid envelope")
}

func TestCloseIsIdempotentAndStoreAfterCloseFails(t *testing.T) {
	dataDir := t.TempDir()
	backend, err := jsonl.New(dataDir)
	require.NoError(t, err)

	err = backend.Close(t.Context())
	require.NoError(t, err)

	// Idempotent
	err = backend.Close(t.Context())
	require.NoError(t, err)

	env := testEnvelope(t, "after-close")
	err = backend.Store(t.Context(), env)
	require.ErrorIs(t, err, jsonl.ErrClosed)
}

func TestReopeningAppendsRatherThanTruncates(t *testing.T) {
	dataDir := t.TempDir()

	// First run
	b1, err := jsonl.New(dataDir)
	require.NoError(t, err)
	env1 := testEnvelope(t, "event-1")
	require.NoError(t, b1.Store(t.Context(), env1))
	require.NoError(t, b1.Close(t.Context()))

	// Reopen
	b2, err := jsonl.New(dataDir)
	require.NoError(t, err)
	env2 := testEnvelope(t, "event-2")
	require.NoError(t, b2.Store(t.Context(), env2))
	require.NoError(t, b2.Close(t.Context()))

	content, err := os.ReadFile(filepath.Join(dataDir, "events", "events.jsonl"))
	require.NoError(t, err)

	lines := bytes.Split(bytes.TrimSuffix(content, []byte("\n")), []byte("\n"))
	require.Len(t, lines, 2)

	d1, err := events.Decode(lines[0])
	require.NoError(t, err)
	d2, err := events.Decode(lines[1])
	require.NoError(t, err)

	require.Equal(t, env1.ID, d1.ID)
	require.Equal(t, env2.ID, d2.ID)
}

func TestWindowsPathsWithSpacesWork(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "sub dir with spaces", "data root")

	backend, err := jsonl.New(dataDir)
	require.NoError(t, err)

	env := testEnvelope(t, "space-path")
	require.NoError(t, backend.Store(t.Context(), env))
	require.NoError(t, backend.Close(t.Context()))

	filePath := filepath.Join(dataDir, "events", "events.jsonl")
	require.FileExists(t, filePath)

	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	lines := bytes.Split(bytes.TrimSuffix(content, []byte("\n")), []byte("\n"))
	require.Len(t, lines, 1)

	decoded, err := events.Decode(lines[0])
	require.NoError(t, err)
	require.Equal(t, env.ID, decoded.ID)
}
