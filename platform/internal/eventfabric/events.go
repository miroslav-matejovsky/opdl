package eventfabric

import "github.com/miroslav-matejovsky/opdl/platform/internal/events"

// This file is the Event Fabric's own event catalog:
//
//   - platform.event_fabric.ready: a node has connected to its site journal,
//     caught its projections up, attached its handlers, and is about to serve.
//   - platform.event_fabric.stopping: a node has begun a clean shutdown.
//
// The Event Fabric states its own lifecycle through the same journal it carries
// domain events on, so there is one ordered history and no second event path to
// reconcile. It deliberately does not state a completed stop: a closed
// transport cannot durably record its own close, so the absence of a later
// ready is the only honest evidence a node stopped.
//
// Both events are stated by runtime composition, not by the adapter. Readiness
// is a conclusion about the whole node — its journal, its projections, and its
// handlers — and a transport can only report on itself. An adapter that
// announced itself ready when its connection opened would be stating something
// it does not know.

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
	// InstanceRole identifies the primary or standby process that became active.
	InstanceRole string `json:"process_role"`
	// ProcessState is active because only the ownership holder publishes readiness.
	ProcessState string `json:"process_state"`
}

// NewReady builds a node's ready event from its fabric's identity and the
// journal sequence its projections have applied.
func NewReady(info Info, highWater uint64, processRole string) Ready {
	return Ready{Info: info, HighWater: highWater, InstanceRole: processRole, ProcessState: "active"}
}

// EventType returns the event's stable dotted kind.
func (Ready) EventType() events.Type { return TypeReady }

// Stopping states that a node's Event Fabric has begun a clean shutdown.
type Stopping struct {
	// Adapter is the transport adapter's implementation name.
	Adapter string `json:"adapter"`
	// InstanceRole identifies the primary or standby process that is stopping.
	InstanceRole string `json:"process_role"`
	// ProcessState records the process lifecycle state at publication.
	ProcessState string `json:"process_state"`
}

// NewStopping builds the lifecycle event published during active shutdown.
func NewStopping(adapter, processRole string) Stopping {
	return Stopping{Adapter: adapter, InstanceRole: processRole, ProcessState: "stopping"}
}

// EventType returns the event's stable dotted kind.
func (Stopping) EventType() events.Type { return TypeStopping }
