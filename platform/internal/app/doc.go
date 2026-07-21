// Package app composes and runs the platform runtime.
//
// A machine's Primary and Standby Instances contend for one Primary Ownership.
// Only the owner opens storage, attaches durable handlers, publishes readiness,
// and serves domain operations. The other process runs a client-only projector in
// the Passive state.
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
package app
