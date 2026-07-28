package eventstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/machine/eventstore"
)

// The tests in this file are the machine store contract. They are written
// against Appender and Reader and never against a file, so the store the
// machine's instances are expected to end up sharing — SQLite — proves itself
// by being added to implementations and changing nothing else.

// store is both halves of the contract. Production hands out one half at a
// time; a test needs both to say anything about either.
type store interface {
	eventstore.Appender
	eventstore.Reader
}

// openStore opens the store at one fixed location. Calling it again reopens the
// same store, which is how a test continues one across a close.
type openStore func(t *testing.T) store

// implementations is every store the contract runs against, by name.
var implementations = map[string]func(t *testing.T) openStore{
	"file": fileLocation,
}

// fileLocation picks a store file for one test and returns the way to open it.
func fileLocation(t *testing.T) openStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "machine.jsonl")
	return func(t *testing.T) store {
		t.Helper()
		opened, err := eventstore.Open(path)
		require.NoError(t, err)
		t.Cleanup(func() { _ = opened.Close(context.Background()) })
		return opened
	}
}

// runContract runs one contract case against every implementation.
func runContract(t *testing.T, contract func(t *testing.T, open openStore)) {
	t.Helper()
	for name, location := range implementations {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			contract(t, location(t))
		})
	}
}

func TestStoreReplaysWhatWasAppended(t *testing.T) {
	t.Parallel()
	runContract(t, func(t *testing.T, open openStore) {
		opened := open(t)
		require.NoError(t, opened.Append(t.Context(), machineEnvelope("first")))

		entries := replay(t, opened, eventstore.FromStart, 1)
		require.Equal(t, "id-first", entries[0].Envelope.ID)
		require.Equal(t, events.ScopeMachine, entries[0].Envelope.Scope)
		require.JSONEq(t, `{"detail":"first"}`, string(entries[0].Envelope.Data))
	})
}

func TestStoreReplaysInAppendOrderWithoutGaps(t *testing.T) {
	t.Parallel()
	runContract(t, func(t *testing.T, open openStore) {
		opened := open(t)
		for _, detail := range []string{"first", "second", "third"} {
			require.NoError(t, opened.Append(t.Context(), machineEnvelope(detail)))
		}

		entries := replay(t, opened, eventstore.FromStart, 3)
		require.Equal(t, []string{"id-first", "id-second", "id-third"}, identities(entries),
			"entries replay in the order they were appended")
		require.Equal(t, []eventstore.Position{1, 2, 3}, positions(entries),
			"positions count from one with no gaps between consecutive entries")
	})
}

func TestStoreResumesAfterAPosition(t *testing.T) {
	t.Parallel()
	runContract(t, func(t *testing.T, open openStore) {
		opened := open(t)
		for _, detail := range []string{"first", "second", "third"} {
			require.NoError(t, opened.Append(t.Context(), machineEnvelope(detail)))
		}

		entries := replay(t, opened, 2, 1)
		require.Equal(t, []string{"id-third"}, identities(entries),
			"a reader resuming from a position gets what came after it and not the position itself")
		require.Equal(t, []eventstore.Position{3}, positions(entries),
			"positions keep their meaning across reads, so a resumed reader can resume again")
	})
}

func TestStoreFollowsAppendsMadeAfterTheReplay(t *testing.T) {
	t.Parallel()
	runContract(t, func(t *testing.T, open openStore) {
		opened := open(t)
		require.NoError(t, opened.Append(t.Context(), machineEnvelope("before")))

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		results, err := opened.Read(ctx, eventstore.FromStart)
		require.NoError(t, err)

		require.Equal(t, []string{"id-before"}, identities(collect(t, results, 1)))

		require.NoError(t, opened.Append(t.Context(), machineEnvelope("after")))

		followed := collect(t, results, 1)
		require.Equal(t, []string{"id-after"}, identities(followed),
			"a caught-up reader is given what is appended next")
		require.Equal(t, []eventstore.Position{2}, positions(followed),
			"following continues the same numbering, so nothing is skipped or repeated at the seam")
		requireNothingFurther(t, results)
	})
}

func TestStoreContinuesTheSameStoreAfterReopen(t *testing.T) {
	t.Parallel()
	runContract(t, func(t *testing.T, open openStore) {
		first := open(t)
		require.NoError(t, first.Append(t.Context(), machineEnvelope("before-restart")))
		require.NoError(t, first.Close(t.Context()))

		second := open(t)
		require.NoError(t, second.Append(t.Context(), machineEnvelope("after-restart")))

		entries := replay(t, second, eventstore.FromStart, 2)
		require.Equal(t, []string{"id-before-restart", "id-after-restart"}, identities(entries),
			"reopening continues the machine's store rather than starting a new one")
		require.Equal(t, []eventstore.Position{1, 2}, positions(entries),
			"positions continue across a restart, so a passive reader resumes where it stopped")
	})
}

func TestStoreRefusesEnvelopesFromAnotherLevel(t *testing.T) {
	t.Parallel()
	runContract(t, func(t *testing.T, open openStore) {
		opened := open(t)

		for _, scope := range []events.Scope{events.ScopeInstance, events.ScopeSite} {
			err := opened.Append(t.Context(), scopedEnvelope("wrong-level", scope))
			require.ErrorIs(t, err, eventstore.ErrScope, "the machine store holds machine-scoped facts only")
		}

		require.NoError(t, opened.Append(t.Context(), machineEnvelope("kept")))
		entries := replay(t, opened, eventstore.FromStart, 1)
		require.Equal(t, []string{"id-kept"}, identities(entries),
			"a refused envelope leaves nothing behind")
	})
}

func TestStoreRefusesAppendsAfterClose(t *testing.T) {
	t.Parallel()
	runContract(t, func(t *testing.T, open openStore) {
		opened := open(t)
		require.NoError(t, opened.Close(t.Context()))
		require.NoError(t, opened.Close(t.Context()), "closing twice is not an error")

		require.ErrorIs(t, opened.Append(t.Context(), machineEnvelope("late")), eventstore.ErrClosed)
	})
}

func TestStoreKeepsConcurrentAppendsWholeAndSeparate(t *testing.T) {
	t.Parallel()
	runContract(t, func(t *testing.T, open openStore) {
		opened := open(t)

		const writers = 8
		// The failures are collected rather than asserted in the goroutines: a
		// require inside one stops that goroutine and not the test.
		failures := make([]error, writers)
		var wg sync.WaitGroup
		for i := range writers {
			wg.Go(func() {
				failures[i] = opened.Append(t.Context(), machineEnvelope(fmt.Sprintf("writer-%d", i)))
			})
		}
		wg.Wait()
		require.NoError(t, errors.Join(failures...))

		entries := replay(t, opened, eventstore.FromStart, writers)
		require.Len(t, identities(entries), writers)
		require.ElementsMatch(t, expectedIdentities(writers), identities(entries),
			"every concurrent append is stored once and readable on its own")
	})
}

// replay reads want entries from the start of a stream and then ends it, which
// is what a caller that is not following does.
func replay(t *testing.T, s store, from eventstore.Position, want int) []eventstore.Entry {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	results, err := s.Read(ctx, from)
	require.NoError(t, err)
	return collect(t, results, want)
}

// collect receives want entries, failing on a result that carries an error, on
// a stream that ends early, and on a stream that goes quiet.
func collect(t *testing.T, results <-chan eventstore.Result, want int) []eventstore.Entry {
	t.Helper()
	entries := make([]eventstore.Entry, 0, want)
	for len(entries) < want {
		select {
		case result, ok := <-results:
			require.Truef(t, ok, "stream closed after %d of %d entries", len(entries), want)
			require.NoError(t, result.Err)
			entries = append(entries, result.Entry)
		case <-time.After(5 * time.Second):
			require.FailNowf(t, "stream went quiet", "got %d of %d entries", len(entries), want)
		}
	}
	return entries
}

// requireNothingFurther fails if the stream delivers anything more. The wait is
// long enough for a follower that polls to have looked again.
func requireNothingFurther(t *testing.T, results <-chan eventstore.Result) {
	t.Helper()
	select {
	case result := <-results:
		require.FailNowf(t, "unexpected delivery", "stream delivered %+v", result)
	case <-time.After(time.Second):
	}
}

func identities(entries []eventstore.Entry) []string {
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.Envelope.ID)
	}
	return ids
}

func positions(entries []eventstore.Entry) []eventstore.Position {
	found := make([]eventstore.Position, 0, len(entries))
	for _, entry := range entries {
		found = append(found, entry.Position)
	}
	return found
}

func expectedIdentities(writers int) []string {
	ids := make([]string, 0, writers)
	for i := range writers {
		ids = append(ids, fmt.Sprintf("id-writer-%d", i))
	}
	return ids
}

// testOrigin is the complete process identity a test envelope carries.
var testOrigin = events.Origin{
	Machine:        "node",
	MachineProfile: "all-in-one",
	ProcessRole:    "primary",
	PID:            4242,
}

// machineEnvelope is a complete envelope of the level this store holds. Detail
// is what tells one apart from another in an assertion.
func machineEnvelope(detail string) events.Envelope {
	return scopedEnvelope(detail, events.ScopeMachine)
}

func scopedEnvelope(detail string, scope events.Scope) events.Envelope {
	return events.Envelope{
		ID:            "id-" + detail,
		Type:          "platform.test.stored",
		SchemaVersion: events.DefaultSchemaVersion,
		OccurredAt:    time.Date(2026, time.July, 28, 9, 0, 0, 0, time.UTC),
		Source:        "test",
		Severity:      events.DefaultSeverity,
		Scope:         scope,
		Origin:        testOrigin,
		Data:          json.RawMessage(`{"detail":"` + detail + `"}`),
	}
}
