package events

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// stubSink captures records instead of storing them, and can be told to fail.
type stubSink struct {
	mu        sync.Mutex
	records   []Record
	appendErr error
	closeErr  error
	closed    int
}

func (s *stubSink) Append(_ context.Context, record Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.appendErr != nil {
		return s.appendErr
	}
	s.records = append(s.records, record)
	return nil
}

func (s *stubSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed++
	return s.closeErr
}

func (s *stubSink) stored() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Record(nil), s.records...)
}

// fixedRecorder builds a recorder whose envelopes are fully determined: the
// clock ticks one second per event and ids count up, so a test asserts exact
// values without sleeping or matching patterns.
func fixedRecorder() (*Recorder, *stubSink) {
	sink := &stubSink{}
	var ticks int64
	now := func() time.Time {
		ticks++
		return time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC).Add(time.Duration(ticks) * time.Second)
	}
	var ids int
	newID := func() string {
		ids++
		return "id-" + string(rune('a'+ids-1))
	}
	return newRecorder(sink, now, newID), sink
}

func TestRecorderStampsEnvelopeOntoPayload(t *testing.T) {
	recorder, sink := fixedRecorder()

	require.NoError(t, recorder.Record(context.Background(), plainEvent{Detail: "started"}))

	stored := sink.stored()
	require.Len(t, stored, 1)
	require.Equal(t, Meta{
		ID:         "id-a",
		Type:       "platform.test.plain",
		Sequence:   1,
		OccurredAt: time.Date(2026, 7, 15, 10, 0, 1, 0, time.UTC),
		Source:     "test",
	}, stored[0].Meta)
	require.JSONEq(t, `{"detail": "started"}`, string(stored[0].Data))
}

func TestRecorderNumbersEventsMonotonicallyWithinOneRun(t *testing.T) {
	recorder, sink := fixedRecorder()

	for range 3 {
		require.NoError(t, recorder.Record(context.Background(), plainEvent{}))
	}

	stored := sink.stored()
	require.Len(t, stored, 3)
	for i, record := range stored {
		require.Equal(t, uint64(i+1), record.Sequence)
	}
	require.Equal(t, []string{"id-a", "id-b", "id-c"}, []string{stored[0].ID, stored[1].ID, stored[2].ID})
}

func TestRecorderStampsOccurredAtInUTC(t *testing.T) {
	sink := &stubSink{}
	local := time.FixedZone("CEST", 2*60*60)
	occurred := time.Date(2026, 7, 15, 12, 0, 0, 0, local)
	recorder := newRecorder(sink, func() time.Time { return occurred }, func() string { return "id" })

	require.NoError(t, recorder.Record(context.Background(), plainEvent{}))

	stored := sink.stored()
	require.Len(t, stored, 1)
	require.Equal(t, time.UTC, stored[0].OccurredAt.Location())
	require.True(t, stored[0].OccurredAt.Equal(occurred))
}

func TestRecorderStampsNormalizedTagsAndOmitsThemWhenAbsent(t *testing.T) {
	recorder, sink := fixedRecorder()

	require.NoError(t, recorder.Record(context.Background(), taggedEvent{tags: []string{"warning", "warning", " audit "}}))
	require.NoError(t, recorder.Record(context.Background(), plainEvent{}))

	stored := sink.stored()
	require.Len(t, stored, 2)
	require.Equal(t, []string{"audit", "warning"}, stored[0].Tags)
	require.Nil(t, stored[1].Tags)

	encoded, err := json.Marshal(stored[1])
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "tags")
}

// TestRecorderEnvelopeCarriesNoNode pins that the node identity is not repeated
// on every record. A sink states which node it holds; see package jsonl, which
// names its file after it.
func TestRecorderEnvelopeCarriesNoNode(t *testing.T) {
	recorder, sink := fixedRecorder()
	require.NoError(t, recorder.Record(context.Background(), plainEvent{}))

	encoded, err := json.Marshal(sink.stored()[0])
	require.NoError(t, err)
	require.NotContains(t, string(encoded), `"node":`)
}

func TestRecorderReportsSinkFailureWithContext(t *testing.T) {
	sink := &stubSink{appendErr: errors.New("disk gone")}
	recorder := NewRecorder(sink)

	err := recorder.Record(context.Background(), plainEvent{})
	require.ErrorContains(t, err, "events: record platform.test.plain")
	require.ErrorContains(t, err, "disk gone")
}

func TestRecorderRejectsMissingEvent(t *testing.T) {
	recorder, _ := fixedRecorder()
	require.ErrorContains(t, recorder.Record(context.Background(), nil), "event is required")
}

func TestRecorderConcurrentRecordsAreSequencedAndComplete(t *testing.T) {
	recorder, sink := fixedRecorder()

	const writers = 50
	// Errors travel back to the test goroutine: require.NoError inside a
	// goroutine cannot fail the test.
	failures := make(chan error, writers)
	var group sync.WaitGroup
	for range writers {
		group.Go(func() {
			failures <- recorder.Record(context.Background(), plainEvent{})
		})
	}
	group.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}

	// Records reach the sink in sequence order: the recorder serializes stamping
	// and the write together, so a reader of the sink sees no reordering.
	stored := sink.stored()
	require.Len(t, stored, writers)
	for i, record := range stored {
		require.Equal(t, uint64(i+1), record.Sequence)
	}
}

func TestRecorderCloseClosesSinkAndReportsItsError(t *testing.T) {
	sink := &stubSink{}
	require.NoError(t, NewRecorder(sink).Close())
	require.Equal(t, 1, sink.closed)

	failing := &stubSink{closeErr: errors.New("close failed")}
	require.ErrorContains(t, NewRecorder(failing).Close(), "close failed")
}

func TestNopRecorderDiscardsEverything(t *testing.T) {
	var nop NopRecorder
	require.NoError(t, nop.Record(context.Background(), plainEvent{}))
	require.NoError(t, nop.Close())
}

func TestNewIDIsUniqueAndTimeOrdered(t *testing.T) {
	// Ids must not collide even when generated inside one clock tick, which is
	// why they carry random bytes and not the timestamp alone.
	const count = 1000
	seen := make(map[string]struct{}, count)
	previousTime := ""
	for range count {
		id := newID()
		require.Len(t, id, 37, "ids must be fixed width so they sort lexically")
		require.NotContains(t, seen, id, "event ids must be unique")
		seen[id] = struct{}{}

		// The zero-padded time prefix is what makes ids sort in occurrence
		// order; the random suffix only breaks ties within one tick.
		occurredAt := id[:20]
		require.GreaterOrEqual(t, occurredAt, previousTime, "ids must sort in occurrence order")
		previousTime = occurredAt
	}
}
