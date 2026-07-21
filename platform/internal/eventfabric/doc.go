// Package eventfabric is the OPDL Event Fabric: the runtime boundary every OPDL
// service publishes events through and receives them from. It states OPDL
// behavior, not a message broker's API. The NATS JetStream adapter binds this
// contract to the transport, but no domain package
// constructs a subject, a stream, or a consumer.
//
// # Responsibilities
//
// Six roles divide the work. The first three are the runtime's boundary; the
// last three are what a domain implements or receives.
//
//   - Fabric is the whole boundary a runtime owns: publish, replay, live
//     delivery, health, and shutdown. Runtime composition opens one, runs the
//     node's projector and its service handlers on it, waits for catch-up, and
//     closes it. A domain package never holds a Fabric; it is given the narrow
//     Publisher, Projector, or Handler it needs.
//   - Publisher appends one OPDL event to the site journal and returns a Receipt
//     only after the journal has durably accepted it. A returning Publish means
//     the fact is retained and ordered; a failing one means it is not.
//   - Receipt is proof the journal accepted an event. It carries the event ID
//     and the journal Sequence the event was assigned. Correctness never depends
//     on the Receipt: it is an acknowledgement, and a lost Receipt after a
//     durable write is a redelivery to reconcile, not a second fact.
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
// This separation is also what a warm standby process runs on. A machine may run a
// second local process that keeps its projections caught up from the journal but
// holds no active capability. Publishing is active-only: running durable Handlers
// and stating the node's readiness and stopping lifecycle events belong to the
// instance that holds Primary Ownership. Those lifecycle payloads carry the
// operational process role and state while their envelope identity remains
// machine-scoped. A standby runs Projectors only, so it stays current without
// causing a fact or exposing a second listener. See internal/redundancy for the
// fence and the ownership rule.
//
// # Delivery guarantees
//
// Delivery is at least once. Every Projector and Handler must be idempotent.
// Message deduplication in the transport is an optimization that narrows the
// window, never a correctness guarantee: a duplicate can still arrive after any
// deduplication window expires, so identity-based collapse lives in the domain
// reducers and handlers.
//
//   - Publish returns only after durable journal acceptance.
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
// encoding. In summary:
//
//   - The site scope is a stable, transport-safe token derived by hashing the
//     length-prefixed project, environment, and site. Human-readable deployment
//     identity stays in the event envelope; the scope only isolates one site's
//     journal from another's.
//   - An event route is opdl.<site-scope>.event.<domain>.<fact>, where <domain>
//     and <fact> come from parsing the platform.<domain>.<fact> event type.
//   - One journal per site is named OPDL_<UPPER_SITE_SCOPE>_EVENTS and binds
//     exactly opdl.<site-scope>.event.>. It is the site's single ordered event
//     history, and its order is the order every projection uses.
//
// # Journal storage and retention
//
// The journal uses file storage and limits-based retention. Reaching a
// configured limit rejects new events rather than deleting the history a replay
// needs: a refused write is a visible, recoverable failure, whereas silently
// dropping the oldest events makes a full rebuild quietly impossible. Capacity
// is a readiness concern, not a background eviction.
//
// # Lifecycle
//
// One process moves through these phases. Each is bounded by context so no wait
// can hang startup or shutdown.
//
//   - Publish. Build the record's envelope, validate it, derive its route, and
//     append it to the journal. Return a Receipt with the assigned Sequence only
//     after durable acceptance.
//   - Startup catch-up. Start one continuous ordered consumer from the first
//     retained event. Capture the journal high-water Sequence, then wait until
//     the node's Projector has applied that Sequence. The same consumer stays
//     attached for live delivery, so there is no replay-to-live gap and no
//     second subscription to miss events between the two.
//   - Live handling. The attached consumer keeps delivering new events in
//     journal order to the Projector, and durable per-service Handlers react to
//     their routes and publish consequences.
//   - Failure. A decode failure, an unsupported event, or exhausted Handler
//     delivery surfaces as a readiness error. The event stays in the journal and
//     the node stops advancing rather than skipping it.
//   - Shutdown. Stop intake, drain active Handlers within the shutdown bound,
//     and close the transport. A closed transport cannot durably state its own
//     completed close, so shutdown records no terminal event.
//
// # What this boundary is not
//
// It is not a general message bus. It exposes no raw subjects, arbitrary
// headers, queue groups, key-value buckets, object stores, request/reply, or
// direct transport connections. It carries OPDL events, in one ordered site
// journal, to OPDL projectors and handlers, and nothing more.
package eventfabric
