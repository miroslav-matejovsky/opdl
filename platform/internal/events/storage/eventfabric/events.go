package eventfabric

import "github.com/miroslav-matejovsky/opdl/platform/internal/events"

// This file is the Event Fabric's own event catalog:
//
//   - platform.event_fabric.ready: a node has connected to its site journal,
//     caught its projections up, attached its handlers, and is about to serve.
//   - platform.event_fabric.stopping: a node has begun a clean shutdown.

const (
	// TypeReady is stated when a node has connected to its site journal, caught
	// its projections up to a recorded high-water mark, and attached its
	// handlers. It is stated before the node serves its public API.
	TypeReady events.Type = "platform.event_fabric.ready"
	// TypeStopping is stated when a node has begun a clean shutdown, after its
	// handlers have finished their active work and before the transport closes.
	TypeStopping events.Type = "platform.event_fabric.stopping"
)

// Ready states that a node's Event Fabric is ready and the node is about to
// serve. It carries the fabric's identity and storage disposition, and the
// journal sequence the node's projections had applied when it concluded it was
// ready.
type Ready struct {
	Info
	// HighWater is the journal sequence this node's projections had applied when
	// it became ready. It is what "caught up" meant for this node at this start.
	HighWater uint64 `json:"high_water"`
}

// EventType returns the event's stable dotted kind.
func (Ready) EventType() events.Type { return TypeReady }

// Stopping states that a node's Event Fabric has begun a clean shutdown.
type Stopping struct {
	// Adapter is the transport adapter's implementation name.
	Adapter string `json:"adapter"`
}

// EventType returns the event's stable dotted kind.
func (Stopping) EventType() events.Type { return TypeStopping }
