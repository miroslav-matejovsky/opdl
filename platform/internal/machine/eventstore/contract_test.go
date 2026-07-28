package eventstore_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/machine/eventstore"
)

// The tests in this file are the machine store contract. They are written
// against Appender and never against a file, so the store the machine's
// instances are expected to end up sharing — SQLite — proves itself by being
// added to implementations and changing nothing else.

// location is one implementation's store for the length of one test.
type location struct {
	// open opens the store. Calling it again reopens the same store, which is
	// how a test continues one across a close.
	open func(t *testing.T) eventstore.Appender
	// stored is what the store holds, read the way an operator reads it rather
	// than through the contract. Nothing in the platform reads a machine store
	// back, so there is no Reader to assert through and each implementation says
	// for itself what it kept.
	stored func(t *testing.T) []events.Envelope
}

// implementations is every store the contract runs against, by name.
var implementations = map[string]func(t *testing.T) location{
	"file": fileLocation,
}

// fileLocation picks a store file for one test and returns the way to open and
// to read it.
func fileLocation(t *testing.T) location {
	t.Helper()
	path := filepath.Join(t.TempDir(), "machine.jsonl")
	return location{
		open: func(t *testing.T) eventstore.Appender {
			t.Helper()
			opened, err := eventstore.Open(path)
			require.NoError(t, err)
			t.Cleanup(func() { _ = opened.Close(context.Background()) })
			return opened
		},
		stored: func(t *testing.T) []events.Envelope { return readLines(t, path) },
	}
}

// runContract runs one contract case against every implementation.
func runContract(t *testing.T, contract func(t *testing.T, at location)) {
	t.Helper()
	for name, at := range implementations {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			contract(t, at(t))
		})
	}
}

func TestStoreKeepsWhatWasAppended(t *testing.T) {
	t.Parallel()
	runContract(t, func(t *testing.T, at location) {
		opened := at.open(t)
		require.NoError(t, opened.Append(t.Context(), machineEnvelope("first")))

		held := at.stored(t)
		require.Len(t, held, 1)
		require.Equal(t, "id-first", held[0].ID)
		require.Equal(t, events.ScopeMachine, held[0].Scope)
		require.JSONEq(t, `{"detail":"first"}`, string(held[0].Data))
	})
}

func TestStoreKeepsAppendOrder(t *testing.T) {
	t.Parallel()
	runContract(t, func(t *testing.T, at location) {
		opened := at.open(t)
		for _, detail := range []string{"first", "second", "third"} {
			require.NoError(t, opened.Append(t.Context(), machineEnvelope(detail)))
		}

		require.Equal(t, []string{"id-first", "id-second", "id-third"}, identities(at.stored(t)),
			"the machine's story is kept in the order it was stated")
	})
}

func TestStoreContinuesTheSameStoreAfterReopen(t *testing.T) {
	t.Parallel()
	runContract(t, func(t *testing.T, at location) {
		first := at.open(t)
		require.NoError(t, first.Append(t.Context(), machineEnvelope("before-restart")))
		require.NoError(t, first.Close(t.Context()))

		second := at.open(t)
		require.NoError(t, second.Append(t.Context(), machineEnvelope("after-restart")))

		require.Equal(t, []string{"id-before-restart", "id-after-restart"}, identities(at.stored(t)),
			"reopening continues the machine's store rather than starting a new one")
	})
}

func TestStoreRefusesEnvelopesFromAnotherLevel(t *testing.T) {
	t.Parallel()
	runContract(t, func(t *testing.T, at location) {
		opened := at.open(t)

		for _, scope := range []events.Scope{events.ScopeInstance, events.ScopeSite} {
			err := opened.Append(t.Context(), scopedEnvelope("wrong-level", scope))
			require.ErrorIs(t, err, eventstore.ErrScope, "the machine store holds machine-scoped facts only")
		}

		require.NoError(t, opened.Append(t.Context(), machineEnvelope("kept")))
		require.Equal(t, []string{"id-kept"}, identities(at.stored(t)),
			"a refused envelope leaves nothing behind")
	})
}

func TestStoreRefusesAppendsAfterClose(t *testing.T) {
	t.Parallel()
	runContract(t, func(t *testing.T, at location) {
		opened := at.open(t)
		require.NoError(t, opened.Close(t.Context()))
		require.NoError(t, opened.Close(t.Context()), "closing twice is not an error")

		require.ErrorIs(t, opened.Append(t.Context(), machineEnvelope("late")), eventstore.ErrClosed)
	})
}

func TestStoreKeepsConcurrentAppendsWholeAndSeparate(t *testing.T) {
	t.Parallel()
	runContract(t, func(t *testing.T, at location) {
		opened := at.open(t)

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

		held := at.stored(t)
		require.Len(t, held, writers)
		require.ElementsMatch(t, expectedIdentities(writers), identities(held),
			"every concurrent append is stored once and readable on its own")
	})
}

// readLines reads the JSON Lines store back the way an operator would: one
// envelope per line, in the order they were appended. A line that will not
// decode fails the test, because a store nobody can read is the failure this is
// checking for.
func readLines(t *testing.T, path string) []events.Envelope {
	t.Helper()
	file, err := os.Open(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, file.Close()) }()

	var held []events.Envelope
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		envelope, err := events.Decode(line)
		require.NoErrorf(t, err, "store line %d does not decode: %s", len(held)+1, line)
		held = append(held, envelope)
	}
	require.NoError(t, scanner.Err())
	return held
}

func identities(held []events.Envelope) []string {
	ids := make([]string, 0, len(held))
	for _, envelope := range held {
		ids = append(ids, envelope.ID)
	}
	return ids
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
