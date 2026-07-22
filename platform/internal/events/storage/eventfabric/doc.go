// Package eventfabric is the OPDL Event Fabric: the capability contracts
// every OPDL service receives events from and projects model state through.
// It states OPDL behavior, not a message broker's API.
//
// # Responsibilities
//
//   - Fabric is the whole boundary a runtime owns: storage backend, replay, live
//     delivery, health, and shutdown. Runtime composition opens one, runs the
//     node's projector and its service handlers on it, waits for catch-up, and
//     closes it. A domain package never holds a Fabric; it is given the narrow
//     Projector or Handler it needs.
//   - Delivery is one journal event handed to a Projector or Handler, with the
//     Sequence it holds in the journal. The same Delivery may arrive more than
//     once; the Sequence is stable across those redeliveries, so a reducer keyed
//     by identity and a wait keyed by Sequence are both well defined.
//   - Projector replays the journal in order and maintains one node-local query
//     model. It is a read model: applying a Delivery must be pure, ordered by
//     Sequence, and idempotent by event identity. Replaying the whole journal
//     from empty must produce the same model on every node.
//   - Handler reacts to a selected set of routes for one service on one node and
//     may publish resulting events. It is the only role that causes new facts.
//     It must be idempotent under redelivery, and it acknowledges an input only
//     after any resulting event is durably accepted.
//
// A Projector and a Handler are kept separate on purpose. Replaying history must
// rebuild a read model without re-causing the side effects a Handler performs,
// so projection and coordination never share one mechanism.
//
// # Causation
//
// Before a Handler is invoked, the delivery is attached to its context as the
// cause, with events.WithCause. Whatever the Handler publishes with that context
// records what produced it and which workflow both belong to, so a reader can
// follow a reaction back to its input. A Handler never attaches or reads those
// links: it states facts, and what caused it to run is not one of them.
//
// # Its own events
//
// The fabric states two facts of its own, declared in events.go: that a node is
// ready and that one has begun stopping. Both are stated by runtime composition
// rather than by an adapter, because readiness is a conclusion about a whole
// node and a transport can only report on itself.
//
// # Delivery guarantees
//
// Delivery is at least once. Every Projector and Handler must be idempotent.
// Message deduplication in the transport is an optimization that narrows the
// window, never a correctness guarantee: a duplicate can still arrive after any
// deduplication window expires, so identity-based collapse lives in the domain
// reducers and handlers.
//
//   - A Handler acknowledges an input only after its resulting state change or
//     event is complete. An uncertain publication leaves the input
//     unacknowledged for redelivery.
//   - Projection application is idempotent by event ID and domain identity.
//   - A malformed or unsupported event stops catch-up and makes the node
//     unready. It is never skipped: a gap in a replayed journal is a corrupt
//     read model, which is worse than refusing to serve.
//
// # Routes and journals
//
// A Route is the stable OPDL destination of one event type at one site. It is
// derived, never supplied: a domain passes an event, and the Fabric derives the
// route from the event type and the site scope. See route.go for the exact
// encoding.
package eventfabric
