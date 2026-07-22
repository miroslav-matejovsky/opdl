// Package operations records platform events locally, for the operator of one
// process. It is a writer, not an event model: Record stamps a typed payload
// with the process's one events.Factory and writes the same canonical envelope
// the site journal carries.
//
// These events deliberately do not depend on the Event Fabric. Connection loss,
// journal unavailability, and projection failure are precisely the conditions an
// operator needs to observe, so every event is written as one JSON object to the
// process error stream. A configured recorder also appends the same lines to a
// local JSONL file, named after the writing process, for retention and automated
// scenario diagnostics.
//
// # Local, therefore best effort
//
// Record returns nothing. A process that cannot describe what it is doing must
// still do it, so a failure to stamp or write is reported to the error stream
// and never propagated into the operation that caused it. A fact a caller must
// not lose belongs in the site journal, through the Event Fabric publisher,
// which fails the operation instead.
//
// Which events exist is not this package's business. Each package declares the
// facts it can state in its own events.go: internal/app for the runtime,
// internal/redundancy for ownership, and internal/eventfabric/nats for the
// transport.
//
// # One shape on the wire
//
// The writer accepts an events.Envelope and nothing else, so every line it
// produces decodes as one. A broken sink is reported to the other sink as plain
// text rather than as an event, because a reader must be able to decode every
// event line it finds, and a failed write is not a fact about the platform.
package operations
