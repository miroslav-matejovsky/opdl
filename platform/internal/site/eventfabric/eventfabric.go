package eventfabric

import (
	"context"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// Delivery is one envelope and its position in the site's order.
type Delivery struct {
	// Envelope is the distributed fact, exactly as its origin stated it.
	Envelope events.Envelope
	// Sequence is the site's strictly increasing order, counted from one.
	Sequence uint64
}

// Result contains a Delivery or the failure that ended the stream.
type Result struct {
	Delivery Delivery
	Err      error
}

// Consumer is the read half of the site's stream, held by whatever folds or
// reacts to what the site decided.
type Consumer interface {
	// Follow replays unacknowledged deliveries in site order, then follows live
	// until ctx ends. Name is a durable, machine-scoped consumer identity.
	// Delivery is at least once.
	Follow(ctx context.Context, name string) (<-chan Result, error)

	// Ack records that the consumer named name has durably handled every
	// delivery up to and including sequence, so a later Follow resumes after
	// it. Acknowledging a sequence already acknowledged changes nothing.
	Ack(ctx context.Context, name string, sequence uint64) error
}
