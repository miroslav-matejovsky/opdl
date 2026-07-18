// Package redundancy owns the local primary and warm-standby contract.
//
// A machine has one compiled domain identity and one registration vote. The
// primary and standby roles are operational identities only. The primary is
// preferred, but active ownership always comes from the exclusive machine fence.
// A returning primary reclaims ownership through graceful handover, never by
// stealing the fence from a live standby.
//
// Only the fence owner may bind the public API, run durable handlers, publish
// lifecycle readiness, or host the embedded NATS server and storage. The other
// process runs a client-only projector.
//
// The fence is a non-expiring OS file lock on a local filesystem. It is released
// after active resources close or automatically when the process exits. Status
// files report each process role, lifecycle state, PID, projection progress,
// lag, and errors. They support operations but never grant active ownership.
//
// Domain event and durable-handler identities remain machine-scoped. Process
// roles appear only in logs, lifecycle payloads, and local status.
package redundancy
