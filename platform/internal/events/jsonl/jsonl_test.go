package jsonl

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// testNode is the deployment identity the sink names its file after.
var testNode = events.Node{
	Project:     "scenario",
	Environment: "development",
	Site:        "local",
	Machine:     "node",
	Role:        "all-in-one",
}

// open opens a sink for testNode in dir and closes it when the test ends.
func open(t *testing.T, dir string) *Sink {
	t.Helper()
	sink, err := Open(dir, testNode)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })
	return sink
}

// readLines returns the raw lines written to testNode's events file.
func readLines(t *testing.T, dir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, FileName(testNode)))
	require.NoError(t, err)
	trimmed := strings.TrimSuffix(string(data), "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// testRecord builds a record with a distinguishable sequence.
func testRecord(sequence uint64) events.Record {
	return events.Record{
		Meta: events.Meta{
			ID:         "id-a",
			Type:       "platform.test.plain",
			Sequence:   sequence,
			OccurredAt: time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC),
			Source:     "test",
		},
		Data: json.RawMessage(`{"detail":"started"}`),
	}
}

func TestFileNameStatesTheNodeOnce(t *testing.T) {
	require.Equal(t, "events-scenario-development-local-node-all-in-one.jsonl", FileName(testNode))
}

func TestFileNameReplacesCharactersThatAreNotPortable(t *testing.T) {
	tests := []struct {
		name string
		node events.Node
		want string
	}{
		{
			name: "separators and spaces",
			node: events.Node{Project: "customer a/b", Environment: "dev", Site: "site:1", Machine: "node\\2", Role: "all-in-one"},
			want: "events-customer-a-b-dev-site-1-node-2-all-in-one.jsonl",
		},
		{
			name: "dots and underscores are kept",
			node: events.Node{Project: "v1.2", Environment: "dev", Site: "site_a", Machine: "node-1", Role: "worker"},
			want: "events-v1.2-dev-site_a-node-1-worker.jsonl",
		},
		{
			name: "empty parts stay empty",
			node: events.Node{Machine: "node"},
			want: "events----node-.jsonl",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, FileName(test.node))
		})
	}
}

func TestOpenKeepsNodesInSeparateFiles(t *testing.T) {
	dir := t.TempDir()
	first := open(t, dir)
	other := events.Node{Project: "scenario", Environment: "development", Site: "local", Machine: "node-b", Role: "worker"}
	second, err := Open(dir, other)
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })

	require.NoError(t, first.Append(context.Background(), testRecord(1)))
	require.NoError(t, second.Append(context.Background(), testRecord(2)))

	// One file per node, so a directory holding several nodes needs no identity
	// on the records themselves.
	require.Len(t, readLines(t, dir), 1)
	data, err := os.ReadFile(filepath.Join(dir, FileName(other)))
	require.NoError(t, err)
	require.Contains(t, string(data), `"sequence":2`)
	require.NotContains(t, string(data), `"sequence":1`)
}

func TestOpenCreatesDirectoryAndFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "events")
	open(t, dir)

	require.FileExists(t, filepath.Join(dir, FileName(testNode)))
}

func TestOpenReportsUnusableDirectoryWithContext(t *testing.T) {
	// A path that is a file cannot become a directory: this is how a bad
	// events_dir is caught at startup.
	path := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))

	_, err := Open(path, testNode)
	require.ErrorContains(t, err, "create events directory")
	require.ErrorContains(t, err, path)
}

func TestAppendWritesOneCompactLinePerRecord(t *testing.T) {
	dir := t.TempDir()
	sink := open(t, dir)

	require.NoError(t, sink.Append(context.Background(), testRecord(1)))
	require.NoError(t, sink.Append(context.Background(), testRecord(2)))

	lines := readLines(t, dir)
	require.Len(t, lines, 2)
	for _, line := range lines {
		require.NotContains(t, line, "\n")
		var decoded events.Record
		require.NoError(t, json.Unmarshal([]byte(line), &decoded))
	}
	// The record carries no node: the file name already states it.
	require.JSONEq(t, `{
		"id": "id-a",
		"type": "platform.test.plain",
		"sequence": 1,
		"occurred_at": "2026-07-15T10:00:00Z",
		"source": "test",
		"data": {"detail": "started"}
	}`, lines[0])
}

func TestAppendFlushesBeforeReturning(t *testing.T) {
	dir := t.TempDir()
	sink := open(t, dir)

	require.NoError(t, sink.Append(context.Background(), testRecord(1)))

	// Readable without closing the sink: a live scenario observes the event.
	require.Len(t, readLines(t, dir), 1)
}

func TestAppendKeepsExistingFileContent(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir, testNode)
	require.NoError(t, err)
	require.NoError(t, first.Append(context.Background(), testRecord(1)))
	require.NoError(t, first.Close())

	// A restart continues the same node's file rather than truncating it.
	second := open(t, dir)
	require.NoError(t, second.Append(context.Background(), testRecord(2)))

	require.Len(t, readLines(t, dir), 2)
}

func TestAppendIsSafeForConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	sink := open(t, dir)

	const writers = 50
	failures := make(chan error, writers)
	var group sync.WaitGroup
	for i := range writers {
		group.Go(func() {
			failures <- sink.Append(context.Background(), testRecord(uint64(i+1)))
		})
	}
	group.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}

	// Every record is one whole, well-formed line: no writer interleaved with
	// another, and every sequence arrived exactly once.
	lines := readLines(t, dir)
	require.Len(t, lines, writers)
	seen := make(map[uint64]int, writers)
	for _, line := range lines {
		var decoded events.Record
		require.NoError(t, json.Unmarshal([]byte(line), &decoded))
		seen[decoded.Sequence]++
	}
	require.Len(t, seen, writers)
	for sequence, count := range seen {
		require.Equal(t, 1, count, "sequence %d written more than once", sequence)
	}
}

func TestCloseIsIdempotentAndRejectsLaterAppends(t *testing.T) {
	dir := t.TempDir()
	sink, err := Open(dir, testNode)
	require.NoError(t, err)

	require.NoError(t, sink.Append(context.Background(), testRecord(1)))
	require.NoError(t, sink.Close())
	require.NoError(t, sink.Close(), "close must be safe next to a deferred close")

	// A record after close is reported, never dropped silently.
	err = sink.Append(context.Background(), testRecord(2))
	require.ErrorContains(t, err, "append to closed events file")
	require.ErrorContains(t, err, FileName(testNode))
	require.Len(t, readLines(t, dir), 1)
}

func TestAppendReportsEncodingFailureWithFileContext(t *testing.T) {
	dir := t.TempDir()
	sink := open(t, dir)

	broken := testRecord(1)
	broken.Data = json.RawMessage(`{invalid`)
	err := sink.Append(context.Background(), broken)
	require.ErrorContains(t, err, "encode event for")
	require.ErrorContains(t, err, dir)
	require.Empty(t, readLines(t, dir))
}

func TestCloseFlushesBufferedContent(t *testing.T) {
	dir := t.TempDir()
	sink, err := Open(dir, testNode)
	require.NoError(t, err)

	// Bypass Append's flush to prove Close does not lose buffered bytes.
	sink.writer = bufio.NewWriter(sink.file)
	_, err = sink.writer.WriteString("{\"sequence\":1}\n")
	require.NoError(t, err)
	require.Empty(t, readLines(t, dir))

	require.NoError(t, sink.Close())
	require.Len(t, readLines(t, dir), 1)
}
