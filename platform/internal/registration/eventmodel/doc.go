// Package eventmodel is the event-sourced registration model frozen in Stage 1
// of the NATS migration (docs/plan/01-event-model.md). It defines the
// registration event catalog and the pure projection that reads it, with no
// dependency on any transport or on shared distributed state.
//
// It exists alongside the current Olric-backed registration package rather than
// replacing it: Stage 3 (docs/plan/03-registration-projections.md) promotes this
// model into the registration runtime and deletes the collection-based store and
// reconciler. Keeping the new model in its own package lets Stage 1 fix the
// contract and prove the reducers without disturbing the live flow, and without
// two event types of the same name in one package.
//
// # The event flow
//
// A registration is a site-wide decision expressed entirely as ordered events in
// the site journal. No query reads distributed state; every view is a
// deterministic projection of these facts:
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
// # Projection
//
// Projection is a pure, ordered, idempotent fold of these events into node-local
// maps. Applying the whole journal from empty rebuilds the same views on every
// node, and a redelivered event changes nothing. The reducers are exercised
// directly in tests without any transport; Apply is the single entry point the
// Event Fabric drives in later stages.
package eventmodel
