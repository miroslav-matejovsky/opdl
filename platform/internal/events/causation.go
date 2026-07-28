package events

import "context"

// This file is infrastructure, not domain vocabulary. Causal links say which
// event's handling produced another one and which workflow both belong to.
// They are envelope metadata, so the platform carries them on the context that
// flows from a delivery into the publications that delivery causes.
//
// Only the Event Fabric calls WithCause, once, before it invokes a handler.
// Business handlers never attach or read causal links: a handler states facts,
// and what caused it to run is not one of them.

type causeKey struct{}

// cause is the causal metadata one delivery contributes to whatever it causes.
type cause struct {
	causationID   string
	correlationID string
}

// WithCause returns a context that attributes every event stamped under it to
// the handling of causing. It is called by the Event Fabric before a handler
// runs, so a handler's publications record their cause without the handler
// knowing that causation exists.
//
// The causation link is the causing occurrence itself. The correlation link is
// the workflow the causing event already belongs to, or the causing occurrence
// when it starts one, so every event of one workflow shares the identity of the
// event that began it.
func WithCause(ctx context.Context, causing Envelope) context.Context {
	correlationID := causing.CorrelationID
	if correlationID == "" {
		correlationID = causing.ID
	}
	return context.WithValue(ctx, causeKey{}, cause{causationID: causing.ID, correlationID: correlationID})
}

// causalLinks returns the causal links ctx carries, both empty when ctx is an
// ordinary command context rather than a handler's. An event that begins a
// chain records neither link.
func causalLinks(ctx context.Context) (causationID, correlationID string) {
	links, _ := ctx.Value(causeKey{}).(cause)
	return links.causationID, links.correlationID
}
