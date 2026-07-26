// Package registration owns the registration use case: proposals, per-node
// decisions, site acceptance, and the conflict view. It is event-sourced. Every
// fact is an ordered event in the site journal, and every answer is a
// deterministic projection of those facts; nothing reads shared state, and no
// node coordinates another.
//
// It depends on the Event Fabric contract only. It publishes through a narrow
// Publisher, folds deliveries through a Projector, and reacts through a Handler.
// It never constructs a subject, stream, or consumer, and it does not know which
// transport carries the journal.
//
// The four facts it can state are declared in events.go, with the identities
// that make them idempotent in identifiers.go. A call site constructs a typed
// payload and publishes it; the envelope around it — occurrence identity, time,
// origin, severity, causal links — is stamped by the runtime's one factory and
// never by this package.
//
// # The event flow
//
// A registration is a site-wide decision expressed entirely as ordered events:
//
//   - platform.registration.proposed: the origin proposes a registration after
//     validating the HTTP input, the trusted origin identity, and the expected
//     machine set. The first proposed event for a unit key in journal order
//     claims that key.
//   - platform.registration.confirmed: one expected node accepts the claiming
//     proposal.
//   - platform.registration.rejected: one expected node refuses a proposal, or a
//     later proposal loses the key to an earlier one. Tagged as a warning.
//   - platform.registration.accepted: the origin commits the registration once
//     every expected node has confirmed the claiming proposal.
//
// Confirmation is required from every expected machine, including the origin.
// The expected set is the static descriptor's, not a quorum and not the
// currently reachable members, so an expected machine that is offline keeps a
// proposal pending indefinitely and says so.
//
// # Identity, not occurrence
//
// A proposal is identified by its proposal ID: a hash of the versioned canonical
// request fields, the trusted origin identity, and the ordered expected
// machines. Two identical proposals share one ID and are the same claim, so a
// retry is idempotent; any different proposal has a different ID and is a
// separate contender. A decision is identified by a decision ID derived from the
// proposal ID, the decision kind, and the deciding machine, so a node that
// republishes its decision after redelivery collapses to the one decision it
// already made, even after any transport deduplication window has expired.
//
// Occurrence time and journal position are transport metadata. They are never
// part of a proposal or decision ID, so the same fact always hashes to the same
// identity regardless of when or in what order it was delivered.
//
// The local process role is never part of these identities either.
// Proposal IDs, decision IDs, and the durable handler's consumer name are
// machine-scoped, so a machine running a warm standby second process (see
// internal/machine/redundancy) still confirms once and decides once. A role is lifecycle
// and diagnostics, not a second voter.
//
// # Ordering and conflict
//
// Order is the site journal order, delivered as eventfabric.Delivery.Sequence.
// It is when the journal accepted an event, not when a client began its request.
// The first proposed event for a key in that order permanently claims the key.
// An identical later proposal is a retry; a different later proposal is a
// rejected conflict contender. A node rejecting the claiming proposal marks that
// proposal rejected but does not release the key: key release is a separate
// future domain event, not implicit cleanup.
//
// # Projection and coordination
//
// Projection is a pure, ordered, idempotent fold of these events into node-local
// maps. Applying the whole journal from empty rebuilds the same views on every
// node, and a redelivered event changes nothing. The reducers are exercised
// directly in tests without any transport. CommandService publishes proposals,
// QueryService reads only Projection, and Handler waits for projection progress
// before publishing a deterministic confirmation, rejection, or acceptance.
//
// # Query views
//
// Expected-machine IP addresses are display metadata resolved from the answering
// node's current deployment descriptor. They are not registration facts and are
// not persisted in events. If a historical machine is absent from the current
// descriptor, queries keep the machine identity and return an empty IP address.
package registration
