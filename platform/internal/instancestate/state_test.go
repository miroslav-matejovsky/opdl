package instancestate_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/instancestate"
)

// stateFile is a path under a fresh temporary directory. The file itself is not
// created: an absent state file is an instance's first ever start, which is the
// case most of these tests begin from.
func stateFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "state.json")
}

func TestOpenRequiresAPath(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"", "   "} {
		_, err := instancestate.Open(path)
		require.ErrorIs(t, err, instancestate.ErrInvalidPath)
	}
}

// A state file that does not exist yet is the instance's first start, not a
// failure: nothing has run to write one.
func TestOpenStartsAtEpochZeroWhenTheFileIsAbsent(t *testing.T) {
	t.Parallel()
	path := stateFile(t)

	store, err := instancestate.Open(path)
	require.NoError(t, err)
	require.Equal(t, uint64(0), store.Epoch())
	require.Equal(t, path, store.Path())
	// Opening is not an incarnation, so nothing is written until Advance.
	require.NoFileExists(t, path)
}

// Open creates the parent directory, so an instance whose blueprint authored a
// state file under a directory nothing has made yet still starts.
func TestOpenCreatesTheParentDirectory(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "deeper", "state.json")

	store, err := instancestate.Open(path)
	require.NoError(t, err)
	_, err = store.Advance()
	require.NoError(t, err)
	require.FileExists(t, path)
}

// The epoch rises by exactly one per incarnation, which is the whole contract.
func TestAdvanceRaisesTheEpochByOne(t *testing.T) {
	t.Parallel()
	store, err := instancestate.Open(stateFile(t))
	require.NoError(t, err)

	for want := uint64(1); want <= 3; want++ {
		got, err := store.Advance()
		require.NoError(t, err)
		require.Equal(t, want, got)
		require.Equal(t, want, store.Epoch())
	}
}

// This is the case the file exists for: a crashed or restarted instance resumes
// from the epoch its previous incarnation reached rather than from zero.
func TestAdvanceContinuesAcrossRestarts(t *testing.T) {
	t.Parallel()
	path := stateFile(t)

	first, err := instancestate.Open(path)
	require.NoError(t, err)
	_, err = first.Advance()
	require.NoError(t, err)
	epoch, err := first.Advance()
	require.NoError(t, err)
	require.Equal(t, uint64(2), epoch)

	// A new process opening the same file: nothing is carried in memory.
	second, err := instancestate.Open(path)
	require.NoError(t, err)
	require.Equal(t, uint64(2), second.Epoch())
	epoch, err = second.Advance()
	require.NoError(t, err)
	require.Equal(t, uint64(3), epoch)
}

// Two instances of one machine keep unrelated counters, because they are two
// independent runtimes with independent incarnations.
func TestStoresAreIndependentPerInstance(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	primary, err := instancestate.Open(filepath.Join(dir, "primary", "state.json"))
	require.NoError(t, err)
	standby, err := instancestate.Open(filepath.Join(dir, "standby", "state.json"))
	require.NoError(t, err)

	for range 3 {
		_, err := primary.Advance()
		require.NoError(t, err)
	}
	epoch, err := standby.Advance()
	require.NoError(t, err)

	require.Equal(t, uint64(3), primary.Epoch())
	require.Equal(t, uint64(1), epoch)
}

// A record that does not decode must not be read as an absent one: that would
// restart the counter at an epoch already handed out.
func TestOpenReportsAnUndecodableRecord(t *testing.T) {
	t.Parallel()
	path := stateFile(t)
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o644))

	_, err := instancestate.Open(path)
	require.ErrorContains(t, err, "decode state")
}

// The file on disk is what the next incarnation reads, so it carries the epoch
// rather than only the in-memory store doing so.
func TestAdvancePersistsTheEpoch(t *testing.T) {
	t.Parallel()
	path := stateFile(t)
	store, err := instancestate.Open(path)
	require.NoError(t, err)
	_, err = store.Advance()
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.JSONEq(t, `{"epoch":1}`, string(data))
}

// A caller told the write failed has not been given a new epoch, so the store
// must not have moved either: the next attempt has to produce the same number.
func TestAdvanceKeepsTheEpochWhenTheWriteFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	store, err := instancestate.Open(path)
	require.NoError(t, err)

	// A directory where the state file should be: the atomic replace cannot
	// overwrite it, and nothing else about the store has changed.
	require.NoError(t, os.Mkdir(path, 0o755))

	_, err = store.Advance()
	require.ErrorContains(t, err, "write state")
	require.Equal(t, uint64(0), store.Epoch())
}
