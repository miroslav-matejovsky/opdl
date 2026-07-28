package eventstore_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/machine/eventstore"
)

// These tests are about the JSON Lines file and not about the contract: what a
// reader does with a line the writer has not finished, and what it does with a
// line nothing can decode. Both are properties of a shared file, so they belong
// with the implementation that has one.

func TestFileWaitsForALineTheWriterHasNotFinished(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "machine.jsonl")
	opened, err := eventstore.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = opened.Close(context.Background()) })

	complete, err := events.Encode(machineEnvelope("interrupted"))
	require.NoError(t, err)
	half := len(complete) / 2

	writeBytes(t, path, complete[:half])

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	results, err := opened.Read(ctx, eventstore.FromStart)
	require.NoError(t, err)
	requireNothingFurther(t, results)

	writeBytes(t, path, append(complete[half:], '\n'))

	entries := collect(t, results, 1)
	require.Equal(t, []string{"id-interrupted"}, identities(entries),
		"the line is delivered whole once the writer has finished it")
}

func TestFileEndsTheStreamOnALineNothingCanDecode(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "machine.jsonl")
	opened, err := eventstore.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = opened.Close(context.Background()) })

	require.NoError(t, opened.Append(t.Context(), machineEnvelope("readable")))
	writeBytes(t, path, []byte("{\"id\":\"truncated\"}\n"))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	results, err := opened.Read(ctx, eventstore.FromStart)
	require.NoError(t, err)

	require.Equal(t, []string{"id-readable"}, identities(collect(t, results, 1)))

	select {
	case result, ok := <-results:
		require.True(t, ok, "the failure is delivered rather than the stream just closing")
		require.Error(t, result.Err, "a line nothing can decode is store corruption a reader has to be told about")
		require.Contains(t, result.Err.Error(), "position 2", "the failure names where reading stopped")
	case <-time.After(5 * time.Second):
		require.FailNow(t, "stream went quiet instead of reporting the corrupt line")
	}

	_, ok := <-results
	require.False(t, ok, "the stream ends after the failure")
}

func TestOpenRejectsAnEmptyPath(t *testing.T) {
	t.Parallel()
	_, err := eventstore.Open("   ")
	require.ErrorIs(t, err, eventstore.ErrInvalidPath)
}

func TestOpenReportsTheFileTheMachineShares(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "machine.jsonl")
	opened, err := eventstore.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = opened.Close(context.Background()) })

	require.Equal(t, path, opened.Path(), "the store reports the path it was given whole")
	require.FileExists(t, path, "opening creates the file and its directory, so a reader has something to follow")
}

// writeBytes appends raw bytes to the store file, standing in for a writer that
// this process does not control.
func writeBytes(t *testing.T, path string, data []byte) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	require.NoError(t, err)
	_, writeErr := file.Write(data)
	require.NoError(t, writeErr)
	require.NoError(t, file.Close())
}
