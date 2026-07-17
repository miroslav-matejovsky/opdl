package events

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Sink is the durable write side for events. Implementations must accept a
// Record durably before returning, so a caller that observes a completed Record
// can rely on the event being readable. A sink is told which Node its events
// are about when it is opened, not per record.
type Sink interface {
	// Append durably stores one record.
	Append(ctx context.Context, record Record) error
	// Close releases the sink's resources.
	Close() error
}

// Recorder stamps events with an envelope and appends them to a Sink. It is
// safe for concurrent use.
//
// Recording is synchronous and serialized: Record holds the recorder's lock
// across both stamping and the sink write, so records reach the sink in the
// order they were recorded and a returning Record means the event is durable.
// Callers pay the sink's write cost; the volume this stage emits does not
// justify a background queue.
//
// The recorder holds this process's own Node and stamps it onto every record.
// Identity is the platform's compiled-in descriptor, so an event's node is
// never something a caller can claim to be.
type Recorder struct {
	node  Node
	sink  Sink
	now   func() time.Time
	newID func() string

	mu sync.Mutex
}

// NewRecorder builds a recorder that stamps an envelope onto every event and
// appends it to sink. Every record carries node, this process's own deployment
// identity. The recorder takes ownership of sink and closes it on Close.
func NewRecorder(node Node, sink Sink) *Recorder {
	return newRecorder(node, sink, time.Now, newID)
}

// newRecorder builds a recorder with injectable time and identity, so tests can
// assert exact envelopes without depending on the clock or randomness.
func newRecorder(node Node, sink Sink, now func() time.Time, newID func() string) *Recorder {
	return &Recorder{node: node, sink: sink, now: now, newID: newID}
}

// Record stamps event with a fresh envelope and appends it to the sink. It
// returns an error with context when the payload cannot be encoded or the sink
// rejects the record; the caller decides how to surface that, and no state is
// rolled back on its behalf.
func (r *Recorder) Record(ctx context.Context, event Event) error {
	if event == nil {
		return errors.New("events: event is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	record, err := newRecord(Meta{
		ID:            r.newID(),
		Type:          event.EventType(),
		SchemaVersion: eventSchemaVersion(event),
		OccurredAt:    r.now().UTC(),
		Source:        event.Source(),
		Node:          r.node,
		Tags:          eventTags(event),
	}, event)
	if err != nil {
		return fmt.Errorf("events: %w", err)
	}
	if err := r.sink.Append(ctx, record); err != nil {
		return fmt.Errorf("events: record %s: %w", record.Type, err)
	}
	return nil
}

// Close closes the underlying sink. Recording after Close fails rather than
// discarding events silently.
func (r *Recorder) Close() error { return r.sink.Close() }

// NopRecorder discards every event. It is the recorder the runtime composes
// when no events directory is configured, and the one tests use when they do
// not inspect events.
type NopRecorder struct{}

// Record discards event and always succeeds.
func (NopRecorder) Record(context.Context, Event) error { return nil }

// Close releases nothing and always succeeds.
func (NopRecorder) Close() error { return nil }

// newID returns a time-ordered unique event id: the occurrence time in
// nanoseconds, zero-padded so ids sort lexically, plus random bytes to break
// ties within one clock tick. The clock alone is too coarse to be unique.
func newID() string {
	// crypto/rand.Read never returns an error: it fills the buffer entirely or
	// the program cannot obtain randomness at all.
	var suffix [8]byte
	_, _ = rand.Read(suffix[:])
	return fmt.Sprintf("%020d-%s", time.Now().UnixNano(), hex.EncodeToString(suffix[:]))
}
