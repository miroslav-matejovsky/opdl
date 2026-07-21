// Package app composes and runs the platform runtime.
//
// The Primary and Standby Instances contend for one Primary Ownership. Only the
// owner opens storage, attaches durable handlers, publishes readiness, and
// serves the public API. The other process runs a client-only projector in the Passive state. Each
// process writes local status and stops if that status cannot be maintained. A
// Standby Instance in the Passive state waits for ownership independently of its projector and recomposes the
// active runtime after acquiring it. A returning Primary Instance uses the same path
// after an operator-initiated failback when the Standby Instance is Active.
//
// App owns startup, readiness, lag enforcement, and ordered shutdown. Domain
// packages receive narrow Event Fabric and registration contracts and never
// configure the transport directly.
package app
