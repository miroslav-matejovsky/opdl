package eventstore_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/machine/eventstore"
)

// These tests are about the JSON Lines file rather than about the contract: the
// path a machine's instances are pointed at, and what opening it does on disk.

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
	require.FileExists(t, path, "opening creates the file and its directory, so the machine's store exists from the start")
}
