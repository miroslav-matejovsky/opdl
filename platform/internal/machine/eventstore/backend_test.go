package eventstore_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/machine/eventstore"
)

// recordingAppender is a machine store that remembers what reached it.
type recordingAppender struct {
	appended []events.Envelope
	closed   int
}

func (r *recordingAppender) Append(_ context.Context, envelope events.Envelope) error {
	r.appended = append(r.appended, envelope)
	return nil
}

func (r *recordingAppender) Close(context.Context) error {
	r.closed++
	return nil
}

func TestBackendStoresOnlyTheMachinesOwnFacts(t *testing.T) {
	t.Parallel()
	appender := &recordingAppender{}
	backend := eventstore.NewBackend(appender)

	for _, scope := range []events.Scope{events.ScopeInstance, events.ScopeSite, events.ScopeMachine} {
		require.NoError(t, backend.Store(t.Context(), scopedEnvelope(string(scope), scope)))
	}

	require.Equal(t, []string{"id-machine"}, identities(appender.appended),
		"a publisher hands every envelope to every backend; this one keeps the machine's and lets the rest by")
}

func TestBackendClosesTheStoreItWasGiven(t *testing.T) {
	t.Parallel()
	appender := &recordingAppender{}
	backend := eventstore.NewBackend(appender)

	require.NoError(t, backend.Close(t.Context()))
	require.Equal(t, 1, appender.closed, "the backend owns the store it was given and closes it")
}
