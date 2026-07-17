package eventfabric

import "github.com/miroslav-matejovsky/opdl/platform/internal/events"

// The Event Fabric states its own lifecycle through the same journal it carries
// domain events on, so there is one ordered history and no second event path to
// reconcile. It states readiness and the start of shutdown. It deliberately does
// not state a completed stop: a closed transport cannot durably record its own
// close, so the absence of a later ready is the only honest evidence a node
// stopped.
//
// Both events are stated by runtime composition, not by the adapter. Readiness
// is a conclusion about the whole node — its journal, its projections, and its
// handlers — and a transport can only report on itself. An adapter that
// announced itself ready when its connection opened would be stating something
// it does not know.

// lifecycleSource is the subsystem the Event Fabric's own events come from.
const lifecycleSource = "event_fabric"

const (
	// TypeReady is stated when a node has connected to its site journal, caught
	// its projections up to a recorded high-water mark, and attached its
	// handlers. It is stated before the node serves its public API.
	TypeReady events.Type = "platform.event_fabric.ready"
	// TypeStopping is stated when a node has begun a clean shutdown, after its
	// handlers have finished their active work and before the transport closes.
	TypeStopping events.Type = "platform.event_fabric.stopping"
)

// Info is a node's Event Fabric identity and storage disposition, as the adapter
// knows it and the ready event reports it. It is operational metadata for
// whoever reads the journal; no platform behavior depends on it.
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

// NewReady builds a node's ready event from its fabric's identity and the
// journal sequence its projections have applied.
func NewReady(info Info, highWater uint64) Ready {
	return Ready{Info: info, HighWater: highWater}
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
