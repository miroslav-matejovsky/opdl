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
// # Shared endpoints and transfer ordering
//
// The two processes share one set of Event Fabric endpoints rather than owning
// one each. The machine's client and cluster addresses come from its deployment
// descriptor, and whichever process holds the fence binds them.
//
// That is possible because the fence is released only after the active process
// has closed its active resources, including the embedded NATS server. The
// ordering is the invariant:
//
//	active closes HTTP and Event Fabric
//	active embedded NATS stops
//	active releases fence
//	waiter acquires fence
//	waiter opens embedded NATS on the same endpoints
//	waiter catches up and serves HTTP
//
// Sharing has consequences worth stating. The client-only process connects to
// the address the active process is currently serving on, so it is genuinely
// warm. Promotion does not change the address advertised to other machines, so
// nothing the rest of the site was told has to change. A returning primary
// connects to the promoted process without a second endpoint list. And the
// firewall inventory is one client port and at most one cluster port per storage
// machine.
//
// Separate per-slot endpoints are rejected. They double the endpoint inventory
// and require every client and route list to carry both the active and the
// inactive slot's addresses. Giving the standby its own endpoint is what left it
// waiting on an address nothing was listening on.
//
// If a bind fails after fence acquisition, the process records a failed status
// with the bind error. It never falls back to an alternate or random port: the
// endpoint is the machine's identity on the site's network, not a preference.
//
// # The fence
//
// The fence is a non-expiring Windows named mutex, created in the machine-wide
// Global namespace under a name the builder derives from the machine's compiled
// deployment identity. It is released after active resources close, or abandoned
// by the kernel when the process exits.
//
// Ownership is therefore a machine fact rather than a configuration agreement.
// The file lock this replaced was scoped by a runtime directory path, so two
// processes excluded each other only if they had been configured with the same
// one; pointing them at different directories, installing the same package twice
// under different paths, or placing the directory on a network filesystem each
// produced two simultaneous actives, and nothing detected any of them.
//
// Waiting is a kernel wait, so a standby is woken when the holder releases rather
// than on its next polling interval.
//
// The mutex provides mutual exclusion, not a fencing token. Two invariants are
// what make exclusion sufficient: active resources close before ownership is
// released, and ownership lives on one pinned OS thread for the life of the
// process so it cannot be abandoned while resources are still held. See
// utils/winmutex for why the second one is a correctness requirement rather than
// an implementation detail.
//
// Status files report each process role, lifecycle state, PID, projection
// progress, lag, and errors. They support operations but never grant active
// ownership, and the directory holding them is not part of the fence.
//
// Domain event and durable-handler identities remain machine-scoped. Process
// roles appear only in logs, lifecycle payloads, and local status.
package redundancy
