package eventfabric

import (
	"context"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage"
)

// Fabric is the Event Fabric as runtime composition owns it: the storage backend
// for writing completed envelopes, the projector and handler runners, the journal
// high-water query, and health and lifecycle operations.
type Fabric interface {
	storage.Backend

	// RunProjector attaches projector to one continuous ordered consumer, from
	// the first retained event through live delivery. It returns when ctx is
	// canceled or the projector fails, so a caller can make readiness depend on
	// it staying alive.
	RunProjector(ctx context.Context, projector Projector) error

	// RunHandler runs handler as a durable per-service, per-node reaction to its
	// routes, with explicit acknowledgement. It returns when ctx is canceled or
	// the handler's delivery is exhausted.
	RunHandler(ctx context.Context, handler Handler) error

	// HandlerPending returns how many journal events handler's durable consumer
	// has yet to acknowledge, so startup can wait for a node's retained work to
	// drain before it serves. It reports ErrHandlerNotAttached until RunHandler
	// has established the consumer.
	HandlerPending(ctx context.Context, handler Handler) (uint64, error)

	// HighWater returns the Sequence of the last event the journal has accepted.
	// Startup captures it and waits for the projector to reach it before serving.
	HighWater(ctx context.Context) (uint64, error)

	// State reports whether the fabric is connected and its projections have
	// caught up, so readiness can be derived without reaching into the transport.
	State(ctx context.Context) (State, error)

	// Info returns the fabric's identity and storage disposition, which is what a
	// node's ready event reports about the transport it runs on.
	Info() Info

	// Close releases the transport.
	Close(ctx context.Context) error
}

// Info is a node's Event Fabric identity and storage disposition, as the
// adapter knows it. It is operational metadata for whoever reads it, including
// the node's ready event; no platform behavior depends on it.
type Info struct {
	// Adapter is the transport adapter's implementation name.
	Adapter string `json:"adapter"`
	// Server is this node's name within the site's transport cluster.
	Server string `json:"server"`
	// Journal is the site journal's name.
	Journal string `json:"journal"`
	// HostsStorage reports whether this node stores the journal, as opposed to
	// routing to the nodes that do.
	HostsStorage bool `json:"hosts_storage"`
	// Replicas is the site journal's replica count.
	Replicas int `json:"replicas"`
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
	// Envelope is the stored event: its metadata and payload, exactly as the
	// journal accepted it.
	Envelope events.Envelope
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
	// Name is the service name this handler is for. It is a stable, safe token
	// that, with the site scope and machine, names the handler's durable
	// consumer, so a restart resumes the same consumer rather than starting a new
	// one.
	Name() string

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
