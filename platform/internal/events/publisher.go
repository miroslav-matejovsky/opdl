package events

import "context"

// Publisher is the producer-facing interface for publishing typed domain events.
// Implementations stamp the event into an Envelope and store it in configured
// backends.
type Publisher interface {
	Publish(ctx context.Context, event Event) error
}
