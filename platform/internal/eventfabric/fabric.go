package eventfabric

import (
	"context"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// Publisher appends one OPDL event to the site journal. It is the narrow
// capability a command service is given: it can state a fact, and it learns
// nothing about consumers, routes, or transport.
//
// Publish returns only after the journal has durably accepted and ordered the
// event. A returning call means the fact is retained and replayable; a failing
// one means it is not, and the caller must surface that rather than assume the
// event exists.
type Publisher interface {
	// Publish durably appends event to the site journal and returns its Receipt.
	Publish(ctx context.Context, event events.Event) (Receipt, error)
}

// Fabric is the Event Fabric as runtime composition owns it: the publish side,
// the projector and handler runners, the journal high-water query, and the
// health and lifecycle operations. A domain package is never handed a Fabric;
// it receives the narrow Publisher, Projector, or Handler role it needs.
type Fabric interface {
	Publisher

	// RunProjector attaches projector to one continuous ordered consumer, from
	// the first retained event through live delivery. It returns when ctx is
	// canceled or the projector fails, so a caller can make readiness depend on
	// it staying alive.
	RunProjector(ctx context.Context, projector Projector) error

	// RunHandler runs handler as a durable per-service, per-node reaction to its
	// routes, with explicit acknowledgement. It returns when ctx is canceled or
	// the handler's delivery is exhausted.
	RunHandler(ctx context.Context, handler Handler) error

	// HighWater returns the Sequence of the last event the journal has accepted.
	// Startup captures it and waits for the projector to reach it before serving.
	HighWater(ctx context.Context) (uint64, error)

	// State reports whether the fabric is connected and its projections have
	// caught up, so readiness can be derived without reaching into the transport.
	State(ctx context.Context) (State, error)

	// Close stops delivery, drains active handlers within ctx, and releases the
	// transport.
	Close(ctx context.Context) error
}

// Receipt is proof the journal durably accepted a published event. It is an
// acknowledgement, not a source of truth: a lost Receipt after a durable write
// is a redelivery to reconcile by identity, never a second fact to publish.
type Receipt struct {
	// ID is the accepted event's envelope ID.
	ID string
	// Sequence is the position the journal assigned the event. It is the site's
	// common order and is stable for the life of the journal.
	Sequence uint64
}

// Delivery is one journal event handed to a Projector or Handler, together with
// the Sequence it holds in the journal.
//
// The same Delivery may arrive more than once. Sequence is stable across those
// redeliveries, so a reducer that collapses by event identity and a wait that
// blocks until a Sequence is applied are both well defined. Sequence is the
// journal's ordering and deliberately is not copied into the immutable record:
// the fact does not depend on where the transport placed it.
type Delivery struct {
	// Record is the stored event: envelope plus payload.
	Record events.Record
	// Sequence is the event's position in the site journal.
	Sequence uint64
}

// State is the Event Fabric's health as readiness consumes it.
type State struct {
	// Connected reports whether the transport is reachable.
	Connected bool
	// CaughtUp reports whether the node's projector has applied every event up
	// to the high-water mark captured at startup.
	CaughtUp bool
	// HighWater is the last Sequence the journal has accepted.
	HighWater uint64
	// Applied is the last Sequence the node's projector has applied.
	Applied uint64
}

// Projector maintains one node-local read model by applying journal deliveries
// in order. It is replayed from the first retained event at startup and then
// kept attached for live events.
//
// Apply must be pure, ordered by Delivery.Sequence, and idempotent by event
// identity: replaying the whole journal from empty must rebuild the same model,
// and a redelivered event must not change it a second time. A Projector never
// publishes; causing new facts is a Handler's job.
type Projector interface {
	// Apply folds one delivery into the read model. Returning an error stops
	// catch-up and makes the node unready rather than leaving a gap.
	Apply(ctx context.Context, delivery Delivery) error
}

// Handler reacts to a selected set of routes for one service on one node and may
// publish resulting events. It is the only role that causes new facts, so it is
// the role that must be idempotent under redelivery and must acknowledge an
// input only after any resulting event is durably accepted.
type Handler interface {
	// Routes are the exact event routes this handler consumes. A handler lists
	// them explicitly; it never subscribes to a wildcard or reacts to an event
	// type it did not name.
	Routes() []Route

	// Handle processes one delivery for a route this handler consumes. It runs
	// after the node's projection has applied the delivery, so the handler
	// decides from a caught-up local view. Returning nil acknowledges the input;
	// returning an error leaves it unacknowledged for redelivery.
	Handle(ctx context.Context, delivery Delivery) error
}
