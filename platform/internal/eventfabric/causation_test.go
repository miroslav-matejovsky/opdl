package eventfabric

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

func TestCausalContextStartsAndContinuesAWorkflow(t *testing.T) {
	started := Delivery{Record: events.Record{Meta: events.Meta{ID: "proposal"}}}
	cause, correlation := CausalLinks(CausalContext(t.Context(), started))
	require.Equal(t, "proposal", cause)
	require.Equal(t, "proposal", correlation)

	continued := Delivery{Record: events.Record{Meta: events.Meta{ID: "confirmation", CorrelationID: "proposal"}}}
	cause, correlation = CausalLinks(CausalContext(t.Context(), continued))
	require.Equal(t, "confirmation", cause)
	require.Equal(t, "proposal", correlation)
}

func TestCausalLinksAreEmptyWithoutADelivery(t *testing.T) {
	cause, correlation := CausalLinks(context.Background())
	require.Empty(t, cause)
	require.Empty(t, correlation)
}
