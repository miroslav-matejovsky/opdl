package events

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// stubEvent is the smallest thing BestEffort can be asked to state.
type stubEvent struct{}

func (stubEvent) EventType() Type { return "platform.test.stated" }

// stubPublisher records what it was asked to publish and answers with err.
type stubPublisher struct {
	err       error
	published []Event
}

func (p *stubPublisher) Publish(_ context.Context, event Event) error {
	p.published = append(p.published, event)
	return p.err
}

// captureDiagnostics redirects the process error stream this package writes
// publication failures to, and restores it when the test ends.
func captureDiagnostics(t *testing.T) *bytes.Buffer {
	t.Helper()
	previous := diagnostics
	buffer := &bytes.Buffer{}
	diagnostics = buffer
	t.Cleanup(func() { diagnostics = previous })
	return buffer
}

func TestBestEffortStatesTheFactAndSaysNothingWhenItWorks(t *testing.T) {
	out := captureDiagnostics(t)
	publisher := &stubPublisher{}

	BestEffort(publisher).State(t.Context(), stubEvent{})

	require.Len(t, publisher.published, 1)
	require.Empty(t, out.String())
}

func TestBestEffortReportsAPublicationFailureToTheErrorStream(t *testing.T) {
	out := captureDiagnostics(t)
	publisher := &stubPublisher{err: errors.New("jsonl: disk is full")}

	BestEffort(publisher).State(t.Context(), stubEvent{})

	require.Contains(t, out.String(), "platform.test.stated")
	require.Contains(t, out.String(), "jsonl: disk is full")
}

// A failure diagnostic must never travel back through the pipeline that just
// failed, so exactly one publication is attempted however badly it goes.
func TestBestEffortDoesNotRepublishItsOwnFailure(t *testing.T) {
	captureDiagnostics(t)
	publisher := &stubPublisher{err: errors.New("nats: no responders")}

	BestEffort(publisher).State(t.Context(), stubEvent{})

	require.Len(t, publisher.published, 1)
}

func TestBestEffortReportsAMissingPublisherRatherThanPanicking(t *testing.T) {
	out := captureDiagnostics(t)

	require.NotPanics(t, func() { BestEffort(nil).State(t.Context(), stubEvent{}) })

	require.Contains(t, out.String(), "no publisher was composed")
}
