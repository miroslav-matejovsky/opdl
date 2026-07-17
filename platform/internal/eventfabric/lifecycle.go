package eventfabric

import "github.com/miroslav-matejovsky/opdl/platform/internal/events"

// The Event Fabric states its own lifecycle through the same journal it carries
// domain events on, so there is one ordered history and no second event path to
// reconcile. It states readiness and the start of shutdown. It deliberately does
// not state a completed stop: a closed transport cannot durably record its own
// close, so the absence of a later ready is the only honest evidence a node
// stopped.

// lifecycleSource is the subsystem the Event Fabric's own events come from.
const lifecycleSource = "event_fabric"

const (
	// TypeReady is stated when a node's Event Fabric has connected and its site
	// journal is ready to publish and replay.
	TypeReady events.Type = "platform.event_fabric.ready"
	// TypeStopping is stated when a node's Event Fabric has begun a clean
	// shutdown, before it closes the transport.
	TypeStopping events.Type = "platform.event_fabric.stopping"
)

// Ready states that a node's Event Fabric is ready. It names the adapter and the
// site journal, and reports the journal's high-water sequence at that moment.
type Ready struct {
	// Adapter is the transport adapter's implementation name.
	Adapter string `json:"adapter"`
	// Stream is the site journal's name.
	Stream string `json:"stream"`
	// HighWater is the journal's last sequence when the node became ready.
	HighWater uint64 `json:"high_water"`
}

// EventType returns the event's stable dotted kind.
func (Ready) EventType() events.Type { return TypeReady }

// Source returns the subsystem that states the fact.
func (Ready) Source() string { return lifecycleSource }

// Stopping states that a node's Event Fabric has begun a clean shutdown.
type Stopping struct {
	// Adapter is the transport adapter's implementation name.
	Adapter string `json:"adapter"`
}

// EventType returns the event's stable dotted kind.
func (Stopping) EventType() events.Type { return TypeStopping }

// Source returns the subsystem that states the fact.
func (Stopping) Source() string { return lifecycleSource }
