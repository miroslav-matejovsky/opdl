// Package nats provides the NATS JetStream storage backend and Event Fabric
// implementation for OPDL.
//
// # Responsibilities
//
// Backend implements both storage.Backend and eventfabric.Fabric:
//   - Storing completed event envelopes in the site's JetStream journal.
//   - Replaying the site journal in order for node-local projections.
//   - Driving durable per-service handlers with explicit acknowledgements.
//   - Running an embedded NATS server on storage nodes.
package nats
