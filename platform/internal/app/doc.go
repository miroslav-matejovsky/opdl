// Package app composes and runs the platform runtime.
//
// The preferred primary and optional standby share one machine fence. Only the
// fence owner opens storage, attaches durable handlers, publishes readiness, and
// serves the public API. The other process runs a client-only projector. Each
// process writes local status and stops if that status cannot be maintained. A
// standby waits for the fence independently of its projector and recomposes the
// active runtime after acquiring it. A returning primary uses the same path
// after controlled handover from a promoted standby.
//
// App owns startup, readiness, lag enforcement, and ordered shutdown. Domain
// packages receive narrow Event Fabric and registration contracts and never
// configure the transport directly.
package app
