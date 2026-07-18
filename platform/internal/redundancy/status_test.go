package redundancy_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/internal/redundancy"
	"github.com/stretchr/testify/require"
)

func TestStatusWriteReadRoundTrip(t *testing.T) {
	t.Parallel()

	path := redundancy.StatusPath(t.TempDir(), "p", "e", "s", "m", redundancy.SlotB)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))

	want := redundancy.Status{
		Slot:       redundancy.SlotB,
		State:      redundancy.StateStandby,
		PID:        4321,
		Applied:    41,
		HighWater:  42,
		Lag:        "1.5s",
		Promotable: true,
		UpdatedAt:  time.Now().UTC().Truncate(time.Second),
		LastError:  "",
	}
	require.NoError(t, want.Write(path))

	got, err := redundancy.ReadStatus(path)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// TestStatusWriteIsAtomic checks a completed write leaves only the final file,
// not the temporary the atomic rename goes through.
func TestStatusWriteIsAtomic(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := redundancy.StatusPath(dir, "p", "e", "s", "m", redundancy.SlotA)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))

	require.NoError(t, redundancy.Status{Slot: redundancy.SlotA, State: redundancy.StateActive}.Write(path))
	require.FileExists(t, path)
	require.NoFileExists(t, path+".tmp", "the temporary file is renamed away, not left behind")
}

func TestReadStatusMissingFile(t *testing.T) {
	t.Parallel()

	_, err := redundancy.ReadStatus(filepath.Join(t.TempDir(), "absent.status"))
	require.Error(t, err)
}
