package redundancy_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/internal/redundancy"
	"github.com/stretchr/testify/require"
)

// TestStatusPathIsInsideTheInstancesOwnDirectory checks the path is the instance's
// runtime directory and nothing else.
//
// It used to qualify the file by project, machine, and role, because one shared
// directory held every instance on the host. The directory is now authored per
// instance, so two instances are separated by the directory they were given
// rather than by a name the runtime composes.
func TestStatusPathIsInsideTheInstancesOwnDirectory(t *testing.T) {
	t.Parallel()

	primaryDir, standbyDir := t.TempDir(), t.TempDir()
	a := redundancy.StatusPath(primaryDir)
	b := redundancy.StatusPath(standbyDir)

	require.Equal(t, filepath.Join(primaryDir, "process.status"), a)
	require.Equal(t, primaryDir, filepath.Dir(a))
	require.NotEqual(t, a, b, "two instances given their own directories write two files")
}

func TestStatusWriteReadRoundTrip(t *testing.T) {
	t.Parallel()

	path := redundancy.StatusPath(t.TempDir())
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))

	want := redundancy.Status{
		Role:          redundancy.RoleStandby,
		State:         redundancy.StatePassive,
		PID:           4321,
		Applied:       41,
		HighWater:     42,
		Lag:           "1.5s",
		FailoverReady: true,
		UpdatedAt:     time.Now().UTC().Truncate(time.Second),
		LastError:     "",
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
	path := redundancy.StatusPath(dir)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))

	require.NoError(t, redundancy.Status{Role: redundancy.RolePrimary, State: redundancy.StateActive}.Write(path))
	require.FileExists(t, path)
	require.NoFileExists(t, path+".tmp", "the temporary file is renamed away, not left behind")
}

func TestStatusCanBeReplacedWhileDeploymentReadsIt(t *testing.T) {
	path := redundancy.StatusPath(t.TempDir())
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, redundancy.Status{Role: redundancy.RoleStandby, State: redundancy.StatePassive}.Write(path))

	reader, err := os.Open(path)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		done <- redundancy.Status{Role: redundancy.RoleStandby, State: redundancy.StateActivating}.Write(path)
	}()
	require.NoError(t, reader.Close())
	require.NoError(t, <-done)

	status, err := redundancy.ReadStatus(path)
	require.NoError(t, err)
	require.Equal(t, redundancy.StateActivating, status.State)
}

func TestReadStatusMissingFile(t *testing.T) {
	t.Parallel()

	_, err := redundancy.ReadStatus(filepath.Join(t.TempDir(), "absent.status"))
	require.Error(t, err)
}
