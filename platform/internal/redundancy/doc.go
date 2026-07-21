// Package redundancy owns a machine's local high-availability contract:
// Fixed-Role Primary/Standby under a Preferred Primary policy.
//
// A machine runs a Primary Instance and, when its blueprint enables one, a
// Standby Instance. The roles are fixed. Both carry the same compiled machine
// identity, so they remain one registration vote and one set of domain
// identities; the role appears only in logs, lifecycle payloads, and local
// status.
//
// The Primary Instance is preferred, but being Active always comes from holding
// Primary Ownership. A returning Primary Instance takes ownership back through
// graceful handover, never by seizing it from a live Standby Instance.
//
// Only the owner may bind the public API, run durable handlers, publish lifecycle
// readiness, or host the embedded NATS server and storage. The other instance
// runs a client-only projector.
//
// # Primary Ownership
//
// Ownership is a non-expiring Windows named mutex in the machine-wide Global
// namespace, under a name the builder derives from the machine's compiled
// deployment identity. It is released after active resources close, or abandoned
// by the kernel when the process exits. Waiting is a kernel wait, so a waiter is
// woken when the holder releases rather than on a polling interval.
//
// Ownership is therefore a machine fact rather than a configuration agreement.
// The file lock this replaced was scoped by a runtime directory path, so two
// processes excluded each other only if they had been configured with the same
// one. Pointing them at different directories, installing the same package twice
// under different paths, or putting the directory on a network filesystem each
// produced two simultaneous actives, and nothing detected any of them.
//
// The mutex provides mutual exclusion. It is not a token that can be checked by
// anything downstream, and two invariants are what make exclusion sufficient:
// active resources close before ownership is released, and ownership lives on one
// pinned OS thread for the life of the process, so it cannot be abandoned while
// resources are still held. See utils/winmutex for why the second is a
// correctness requirement rather than an implementation detail.
//
// # Shared endpoints and transfer ordering
//
// The two instances share one set of Event Fabric endpoints rather than owning
// one each. The addresses come from the deployment descriptor, and whichever
// instance holds ownership binds them. That works because ownership is released
// only after the active process has closed its active resources, including the
// embedded NATS server:
//
//	active closes HTTP and Event Fabric
//	active embedded NATS stops
//	active releases ownership
//	waiter acquires ownership
//	waiter opens embedded NATS on the same endpoints
//	waiter catches up and serves HTTP
//
// Sharing is what keeps the waiting instance genuinely warm: it connects to the
// address the active process is serving on. A transfer does not move the site
// onto new addresses, and the firewall inventory stays one client port and at
// most one cluster port per storage machine.
//
// Separate per-instance endpoints are rejected. They double the endpoint
// inventory and require every client and route list to carry both. Giving the
// standby its own endpoint is what once left it waiting on an address nothing was
// listening on.
//
// If a bind fails after acquiring ownership, the process records a failed status
// with the bind error. It never falls back to an alternate or random port: the
// endpoint is the machine's identity on the site's network, not a preference.
//
// # Status files
//
// Status files report each instance's role, state, PID, projection progress, lag,
// and errors. They are operational evidence and never grant ownership, and the
// directory holding them takes no part in the ownership decision.
package redundancy
