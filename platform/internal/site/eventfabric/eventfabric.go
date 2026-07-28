package eventfabric

import (
	"context"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// Delivery is one event as the site ordered it: the fact, and where in the
// site's one stream it landed.
//
// It is what a projection folds. Sequence is delivery metadata and belongs to
// the fabric, not to the event, so folding the same Delivery twice has to leave
// a projection where it was.
type Delivery struct {
	// Envelope is the distributed fact, exactly as its origin stated it.
	Envelope events.Envelope
	// Sequence is the site's order, counted from one. It is strictly
	// increasing across one stream, and a consumer that has handled sequence n
	// has handled everything before it.
	Sequence uint64
}

// Result is one read from the fabric: a Delivery, or the failure that ended the
// stream.
//
// A failed stream is reported rather than silently closed because a consumer
// that stops receiving cannot otherwise tell a cancelled follow from a fabric
// it has lost, and those call for opposite reactions: one is shutdown, the
// other is a node that has stopped hearing the site and must not keep answering
// as though it had not.
type Result struct {
	Delivery Delivery
	Err      error
}

// Consumer is the read half of the site's stream, held by whatever folds or
// reacts to what the site decided.
type Consumer interface {
	// Follow delivers the events the durable consumer named name has not
	// handled, in site order, and then follows the stream live, until ctx ends.
	// Replay and live delivery are one stream: there is no seam a consumer has
	// to notice, and none it could handle correctly if there were.
	//
	// The name is the consumer's durable identity and is machine-scoped, not
	// process-scoped: a machine's Primary and Standby instances share one
	// position, so a machine reacts once however many processes it runs.
	//
	// Delivery is at-least-once. A consumer will see an event it has already
	// seen after a restart or a redelivery, so what it does with one has to be
	// idempotent.
	//
	// The returned channel closes when the stream ends, whether because ctx
	// ended or because a Result carrying an error was delivered.
	Follow(ctx context.Context, name string) (<-chan Result, error)

	// Ack records that the consumer named name has durably handled every
	// delivery up to and including sequence, so a later Follow resumes after
	// it. Acknowledging a sequence already acknowledged changes nothing.
	Ack(ctx context.Context, name string, sequence uint64) error
}
