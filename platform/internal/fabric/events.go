package fabric

import "github.com/miroslav-matejovsky/opdl/platform/internal/events"

// This file is the fabric's complete list of events. Each is a fact about this
// machine's fabric membership, recorded by runtime composition at the lifecycle
// transition that makes it true:
//
//   - platform.fabric.started: the fabric is ready to carry traffic. Recorded
//     after readiness and before the public API accepts anything, so an event
//     log shows the platform never served traffic without a fabric.
//   - platform.fabric.stopped: the fabric has been drained and closed. Recorded
//     after HTTP intake stopped and before the event sink closes, so the last
//     thing a log shows is an orderly shutdown.
//
// The payloads describe this machine's own membership. They report operational
// facts about the backend, which is why they name the adapter: it is metadata
// for whoever is reading the log, not a platform guarantee. Nothing here implies
// a fabric capability, and no fabric event carries a peer's address.

// eventSource is the subsystem every event in this file comes from.
const eventSource = "fabric"

// TypeStarted is stated when the fabric is ready to carry traffic.
const TypeStarted events.Type = "platform.fabric.started"

// Started states that this machine's fabric member is ready.
type Started struct {
	// Adapter is the fabric adapter implementation that started. It is
	// operational metadata: the platform's behavior does not depend on it.
	Adapter string `json:"adapter"`
	// Address is this machine's own fabric member address.
	Address string `json:"address"`
	// Members is the number of expected site members reachable at readiness. It
	// is a reading taken at one instant: a member that has not started yet is
	// not counted, and joins later.
	Members int `json:"members"`
}

// EventType returns the event's stable dotted kind.
func (Started) EventType() events.Type { return TypeStarted }

// Source returns the subsystem that states the fact.
func (Started) Source() string { return eventSource }

// TypeStopped is stated once the fabric has been drained and closed.
const TypeStopped events.Type = "platform.fabric.stopped"

// Stopped states that this machine's fabric member has stopped.
type Stopped struct {
	// Adapter is the fabric adapter implementation that stopped.
	Adapter string `json:"adapter"`
}

// EventType returns the event's stable dotted kind.
func (Stopped) EventType() events.Type { return TypeStopped }

// Source returns the subsystem that states the fact.
func (Stopped) Source() string { return eventSource }
