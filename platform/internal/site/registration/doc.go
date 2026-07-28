// Package registration owns site registration proposals, decisions,
// acceptance, and conflict views.
//
// Four site-scoped events form the domain history: proposed, confirmed,
// rejected, and accepted. Proposal and decision IDs are deterministic, so
// retries and at-least-once delivery are idempotent. The first proposal for a
// unit key by eventfabric.Delivery.Sequence claims the key.
//
// Projection is an ordered in-memory fold. CommandService publishes proposals,
// QueryService reads the projection, and Handler publishes deterministic
// decisions after its input is applied.
//
// The projection and query behavior are implemented. CommandService.Create,
// NewHandler, and site distribution are not implemented. See
// internal/site/README.md and docs/03-registration.md.
package registration
