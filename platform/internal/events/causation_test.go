package events

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWithCauseStartsAndContinuesAWorkflow(t *testing.T) {
	starting := Envelope{ID: "proposal"}
	causationID, correlationID := causalLinks(WithCause(t.Context(), starting))
	require.Equal(t, "proposal", causationID)
	require.Equal(t, "proposal", correlationID, "an event that starts a workflow names it after itself")

	continuing := Envelope{ID: "confirmation", CorrelationID: "proposal"}
	causationID, correlationID = causalLinks(WithCause(t.Context(), continuing))
	require.Equal(t, "confirmation", causationID)
	require.Equal(t, "proposal", correlationID, "an ongoing workflow keeps the identity it started with")
}

func TestCausalLinksAreEmptyOutsideAHandler(t *testing.T) {
	causationID, correlationID := causalLinks(context.Background())
	require.Empty(t, causationID)
	require.Empty(t, correlationID)
}
