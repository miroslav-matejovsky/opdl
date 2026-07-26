package instancestate_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/instance/instancestate"
)

// recordedState is the record in testdata: an instance that has been launched
// twice and has served once, so its epoch is three. It is checked in rather than
// built by a test so the decoder is exercised against a file with every field
// spelled out, including the timestamps, which a freshly written record can only
// be compared against loosely.
const recordedState = "testdata/state.json"

// stateFile is a path under a fresh temporary directory. The file itself is not
// created: an absent state file is an instance's first ever start, which is the
// case most of these tests begin from.
func stateFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "state.json")
}

// copyOfRecordedState puts the checked-in record in a temporary directory, so a
// test that advances it writes to its own copy and leaves testdata alone.
func copyOfRecordedState(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(recordedState)
	require.NoError(t, err)
	path := stateFile(t)
	require.NoError(t, os.WriteFile(path, data, 0o644))
	return path
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
	require.Equal(t, instancestate.State{}, store.State(),
		"an instance that has never run has no counters and no times")
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
	_, err = store.Advance(instancestate.ReasonProcessStarted)
	require.NoError(t, err)
	require.FileExists(t, path)
}

// TestOpenDecodesARecordedState reads the checked-in record and checks every
// field arrives, which is what a restarting instance depends on.
func TestOpenDecodesARecordedState(t *testing.T) {
	t.Parallel()
	store, err := instancestate.Open(recordedState)
	require.NoError(t, err)

	state := store.State()
	require.Equal(t, uint64(3), state.Epoch)
	require.Equal(t, uint64(2), state.ProcessEpoch.Count)
	require.Equal(t, uint64(1), state.ActivationEpoch.Count)
	require.Equal(t, "2026-07-26T09:15:42.123456789Z", state.UpdatedAt.Format(time.RFC3339Nano))
	require.Equal(t, state.UpdatedAt, state.ProcessEpoch.UpdatedAt,
		"this record's last advance was the process start, so the two times are the same one")
	require.Equal(t, "2026-07-26T08:04:11.5Z", state.ActivationEpoch.UpdatedAt.Format(time.RFC3339Nano))
	require.Equal(t, time.UTC, state.UpdatedAt.Location())
}

// The epoch rises by exactly one per incarnation whichever kind it is, which is
// the whole contract of the total.
func TestAdvanceRaisesTheEpochByOne(t *testing.T) {
	t.Parallel()
	store, err := instancestate.Open(stateFile(t))
	require.NoError(t, err)

	reasons := []instancestate.Reason{
		instancestate.ReasonProcessStarted,
		instancestate.ReasonActivated,
		instancestate.ReasonActivated,
	}
	for i, reason := range reasons {
		want := uint64(i + 1)
		written, err := store.Advance(reason)
		require.NoError(t, err)
		require.Equal(t, want, written.Epoch)
		require.Equal(t, want, store.Epoch())
	}
}

// Each kind is counted on its own, because the total says an incarnation is a
// different one and never what made it different.
func TestAdvanceCountsEachKindSeparately(t *testing.T) {
	t.Parallel()
	store, err := instancestate.Open(stateFile(t))
	require.NoError(t, err)

	// One launch that then served twice: an instance whose ownership moved back
	// and forth without the process ever dying.
	_, err = store.Advance(instancestate.ReasonProcessStarted)
	require.NoError(t, err)
	for range 2 {
		_, err = store.Advance(instancestate.ReasonActivated)
		require.NoError(t, err)
	}

	state := store.State()
	require.Equal(t, uint64(3), state.Epoch)
	require.Equal(t, uint64(1), state.ProcessEpoch.Count)
	require.Equal(t, uint64(2), state.ActivationEpoch.Count)
}

// A kind that has never advanced carries no time, which is how a reader tells a
// Standby that has only ever followed from one that has served.
func TestAdvanceLeavesTheOtherKindUntouched(t *testing.T) {
	t.Parallel()
	store, err := instancestate.Open(stateFile(t))
	require.NoError(t, err)

	before := time.Now().UTC()
	_, err = store.Advance(instancestate.ReasonProcessStarted)
	require.NoError(t, err)

	state := store.State()
	require.Equal(t, uint64(1), state.ProcessEpoch.Count)
	require.WithinDuration(t, before, state.ProcessEpoch.UpdatedAt, time.Minute)
	require.Equal(t, state.ProcessEpoch.UpdatedAt, state.UpdatedAt,
		"the epoch and the kind that moved it were stamped with the same instant")
	require.Zero(t, state.ActivationEpoch.Count)
	require.True(t, state.ActivationEpoch.UpdatedAt.IsZero(),
		"an instance that has never activated has no activation time to report")
}

// An advance nothing accounts for is refused rather than counted, and it moves
// neither the epoch nor the file.
func TestAdvanceRejectsAnUnknownReason(t *testing.T) {
	t.Parallel()
	path := stateFile(t)
	store, err := instancestate.Open(path)
	require.NoError(t, err)

	_, err = store.Advance(instancestate.Reason("stepped_down"))
	require.ErrorIs(t, err, instancestate.ErrUnknownReason)
	require.Equal(t, uint64(0), store.Epoch())
	require.NoFileExists(t, path)
}

// This is the case the file exists for: a crashed or restarted instance resumes
// from the epoch its previous incarnation reached rather than from zero.
func TestAdvanceContinuesAcrossRestarts(t *testing.T) {
	t.Parallel()
	path := stateFile(t)

	first, err := instancestate.Open(path)
	require.NoError(t, err)
	_, err = first.Advance(instancestate.ReasonProcessStarted)
	require.NoError(t, err)
	written, err := first.Advance(instancestate.ReasonActivated)
	require.NoError(t, err)
	require.Equal(t, uint64(2), written.Epoch)

	// A new process opening the same file: nothing is carried in memory.
	second, err := instancestate.Open(path)
	require.NoError(t, err)
	require.Equal(t, uint64(2), second.Epoch())
	written, err = second.Advance(instancestate.ReasonProcessStarted)
	require.NoError(t, err)
	require.Equal(t, uint64(3), written.Epoch)

	state := second.State()
	require.Equal(t, uint64(2), state.ProcessEpoch.Count, "both launches are counted, across the two processes")
	require.Equal(t, uint64(1), state.ActivationEpoch.Count)
}

// The same continuation, starting from the record checked into testdata rather
// than from one this test wrote: an instance picks up whatever a previous build
// of the runtime left on disk.
func TestAdvanceContinuesFromARecordedState(t *testing.T) {
	t.Parallel()
	store, err := instancestate.Open(copyOfRecordedState(t))
	require.NoError(t, err)

	state, err := store.Advance(instancestate.ReasonActivated)
	require.NoError(t, err)
	require.Equal(t, uint64(4), state.Epoch)
	require.Equal(t, state, store.State(), "the record returned is the one the store now holds")

	require.Equal(t, uint64(2), state.ProcessEpoch.Count, "unmoved, and not recomputed from the epoch")
	require.Equal(t, uint64(2), state.ActivationEpoch.Count)
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
		_, err := primary.Advance(instancestate.ReasonProcessStarted)
		require.NoError(t, err)
	}
	written, err := standby.Advance(instancestate.ReasonProcessStarted)
	require.NoError(t, err)

	require.Equal(t, uint64(3), primary.Epoch())
	require.Equal(t, uint64(1), written.Epoch)
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

// The file on disk is what the next incarnation reads, so it carries the whole
// record rather than only the in-memory store doing so.
func TestAdvancePersistsTheWholeRecord(t *testing.T) {
	t.Parallel()
	path := stateFile(t)
	store, err := instancestate.Open(path)
	require.NoError(t, err)
	_, err = store.Advance(instancestate.ReasonActivated)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	written := store.State()
	stamp := written.UpdatedAt.Format(time.RFC3339Nano)
	require.JSONEq(t, `{
		"epoch": 1,
		"updated_at": "`+stamp+`",
		"process_epoch": {"count": 0, "updated_at": "0001-01-01T00:00:00Z"},
		"activation_epoch": {"count": 1, "updated_at": "`+stamp+`"}
	}`, string(data))
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

	_, err = store.Advance(instancestate.ReasonProcessStarted)
	require.ErrorContains(t, err, "write state")
	require.Equal(t, uint64(0), store.Epoch())
	require.Equal(t, instancestate.State{}, store.State())
}
