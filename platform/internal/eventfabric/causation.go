package eventfabric

import "context"

// CausalContext returns a context that attributes a resulting publication to
// delivery. Handlers use it only for the publish caused by that delivery.
func CausalContext(ctx context.Context, delivery Delivery) context.Context {
	return context.WithValue(ctx, causalContextKey{}, causalLinks{
		causationID:   delivery.Record.ID,
		correlationID: correlationID(delivery),
	})
}

// CausalLinks returns the causal envelope fields carried by ctx. It is intended
// for Event Fabric adapters; domain code should use CausalContext.
func CausalLinks(ctx context.Context) (causationID, correlationID string) {
	links, _ := ctx.Value(causalContextKey{}).(causalLinks)
	return links.causationID, links.correlationID
}

type causalContextKey struct{}

type causalLinks struct {
	causationID   string
	correlationID string
}

func correlationID(delivery Delivery) string {
	if delivery.Record.CorrelationID != "" {
		return delivery.Record.CorrelationID
	}
	return delivery.Record.ID
}
