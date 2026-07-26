// Package app composes and runs the platform runtime.
//
// A machine's Primary and Standby Instances share one Primary Ownership lease.
// Both instances maintain their authored Event Fabric membership and projection.
// Only the owner attaches durable handlers, publishes readiness, and serves
// domain operations. The other process runs only its projector in the Passive
// state.
//
// Both instances bind their own API address for their whole lifetime. A Passive
// instance answers for itself — what it is, what it is doing, and where the other
// instance is — and refuses every domain operation with a pointer to the instance
// that holds ownership. Activation swaps the handler behind a listener that is
// already open, so the address a caller uses never changes and is never briefly
// free. See server.go.
//
// # Where the lifecycle lives
//
// This package supplies the two compositions and the redundancy package sequences
// them: what runs while Passive, what runs while Active, and nothing about when.
// Which instance may run its active composition, when the other must have
// stopped, and how ownership is released are redundancy's, because none of it is
// about what an instance runs.
//
// App owns startup, readiness, lag enforcement, and ordered shutdown. Domain
// packages receive narrow Event Fabric and registration contracts and never
// configure the transport directly.
//
// # Its own events
//
// The runtime states its own facts: what it started, bound, opened, waited for,
// and stopped. They are declared in events.go and stated through the
// process-local publisher, whose only backend is the mandatory local JSONL
// record. They do not reach the site journal, because they describe one
// process — and a process that is failing to start is exactly the one that
// cannot write to a journal.
//
// # Composition owns the publishers
//
// This package composes the process's one events.Factory, from the deployment
// descriptor and the resolved instance role, and the two publishers built on it:
//
//   - the process-local publisher, over the mandatory JSONL record alone. It is
//     what this package, redundancy, and the transport adapter state through.
//   - the site's fan-out publisher, over that same record first and this node's
//     transport second. It is what registration and Event Fabric readiness are
//     handed, and it is how a fact reaches the site.
//
// Every producer is given one of them explicitly and none of them reaches for
// one. Which publisher a component receives decides who hears it, and that
// decision is made here and nowhere else. No producer knows that JSONL or the
// Event Fabric exist.
//
// One factory stamps both, so a local record and a journalled event carry the
// same origin, and a journalled event is in the local file too, in the order the
// process stated it.
//
// The record is opened before anything else and closed last, because it has to
// outlive every site the process composes: a machine that fails over composes
// two.
package app
