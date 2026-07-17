// Package eventmodel is the event-sourced registration implementation built in
// Stages 1 and 3 of the NATS migration. It defines the event catalog, node-local
// projection, command and query services, and durable coordination handler. It
// has no dependency on NATS or shared distributed state.
//
// It exists alongside the current Olric-backed registration package rather than
// replacing it yet because Stage 4 owns runtime composition, startup replay, and
// HTTP activation. Stage 4 promotes this package into the live registration
// boundary and deletes the collection-based store and reconciler in one cutover.
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
// # Projection and coordination
//
// Projection is a pure, ordered, idempotent fold of these events into node-local
// maps. Applying the whole journal from empty rebuilds the same views on every
// node, and a redelivered event changes nothing. The reducers are exercised
// directly in tests without any transport. CommandService publishes proposals,
// QueryService reads only Projection, and Handler waits for projection progress
// before publishing a deterministic confirmation, rejection, or acceptance.
package eventmodel
