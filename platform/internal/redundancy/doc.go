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
// graceful handover, never by seizing it from a Standby Instance in the Active state.
//
// Only the owner may serve domain operations, run durable handlers, publish
// lifecycle readiness, or host the embedded NATS server and storage. The other
// instance (whether Primary or Standby) runs a client-only projector in the
// Passive state, and answers for itself on its own API address.
//
// # What this package decides, and what it does not
//
// This package owns the ownership lifecycle: which of a machine's two instances
// may run its active composition, when the other must have stopped, and what an
// operator is told about the move. It does not know what a composition is. The
// runtime supplies two functions — one to run while Passive, one to run while
// Active — and Contend decides when each runs. See ownership.go.
//
// The sequencing is the point. The two compositions open the same journal
// storage and the same node identity, so an overlap is a machine running two of
// itself, and nothing in the type system prevents it. Contend does: Passive has
// returned before Active is called, and ownership is released only after Active
// has returned.
//
// # Primary Ownership
//
// Ownership is a non-expiring Windows named mutex in the machine-wide Global
// namespace, under a name authored in the machine's blueprint and carried in its
// deployment descriptor. It is released after active resources close, or abandoned
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
// # Per-instance endpoints and transfer ordering
//
// Each instance owns its own endpoints. Every address either of them binds is
// resolved onto that instance's record in the deployment descriptor, and nothing
// on a machine is shared between them except the ownership object, which is not a
// port.
//
// An instance's API address is bound for its whole lifetime rather than only
// while it is Active, so a transfer changes which address serves domain
// operations rather than moving one address between processes:
//
//	both instances are listening, on their own addresses
//	active closes domain serving and Event Fabric
//	active embedded NATS stops
//	active releases ownership
//	waiter acquires ownership
//	waiter opens its own embedded NATS
//	waiter catches up and serves domain operations on the address it already had
//
// The ordering is what makes this safe, and it is the same ordering a shared
// endpoint needed: ownership is released only after the active process has closed
// its active resources. What changed is that the waiter no longer waits for an
// address to be freed, so it cannot be left waiting on an address nothing is
// listening on — the failure this design once had.
//
// A binding failure is a startup failure. The listener opens before an instance
// knows whether it will be Active, so an unusable address stops the process
// immediately rather than at a failover, which is the worst time to find out. It
// never falls back to an alternate or random port: the endpoint is the instance's
// identity, not a preference.
//
// # Status files
//
// Status files report each instance's role (`primary` or `standby`), state (`active` or `passive`), PID, projection progress, lag,
// and errors. They are operational evidence and never grant ownership, and the
// directory holding them takes no part in the ownership decision.
package redundancy
