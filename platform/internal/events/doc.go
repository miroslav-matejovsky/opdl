// Package events defines the platform-wide event contract.
//
// Domain packages declare immutable facts as Event payloads. A process owns one
// Factory, which stamps each payload into a validated Envelope containing its
// occurrence metadata, scope, immutable origin, optional causal links, and JSON
// data. Producers publish payloads through Publisher and do not construct
// envelopes or select storage.
//
// Scope declares the owner of a fact. Every scope reaches the stating
// instance's event log. Machine scope also reaches the machine event store.
// Site scope is reserved for site distribution, which is not implemented yet.
// Instance is the default scope.
//
// Event types use platform.<source>.<fact>. Concrete event catalogs live in the
// package that owns each transition. Optional interfaces override the default
// schema version, severity, scope, tags, or stable identity.
//
// Transport order is delivery metadata and is not part of Envelope. Encode and
// Decode provide the canonical JSON representation.
//
// See docs/02-events.md for destinations, storage, and current implementation
// status.
package events
