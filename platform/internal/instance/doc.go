// Package instance is the platform's local instance and fencing contract: the
// stable process identities a machine runs, the lifecycle states they move
// through, the ownership rule that decides which one serves, and the machine
// fence that enforces it.
//
// It defines terminology and mechanism only. It does not compose the runtime, run
// a standby, or promote one. The app package consumes this contract to run a
// slot; a domain package never sees it.
//
// # Machine and slot
//
// A machine is the existing compiled deployment identity and one registration
// voter. A slot is a stable local process identity on that machine, either A or
// B. A slot is process identity and nothing more: it is never a machine, a
// service, a registration voter, or a permanent primary. Which slot holds the
// active fence can change across restarts, and adding a second slot never adds a
// second machine decision.
//
// The two slots are symmetric. There is no preferred primary: the first healthy
// slot to acquire the machine fence becomes active, so recovery does not wait for
// one particular slot to be present.
//
// # Lifecycle states
//
// A slot moves through the states named by State: starting, standby, activating,
// active, stopping, and failed. Only the active state owns the machine's
// externally visible, decision-producing capabilities. State enumerates the
// legal transitions; runtime code drives them and reports the current state for
// local diagnostics.
//
// # Ownership rule
//
// Exactly one process on a machine may hold the fence, and only the slot holding
// it may:
//
//   - bind the public API,
//   - start the durable domain handlers,
//   - publish machine lifecycle readiness, or
//   - host the embedded NATS server and its storage.
//
// A standby does none of these. It connects to the journal and runs projectors
// only, so it stays caught up without producing a domain decision or exposing a
// second listener. Every active-only capability is gated by the fence; there is
// no capability a standby may hold "just this once".
//
// # The fence
//
// The fence is an exclusive OS file lock in the machine's local runtime
// directory, shared only by the two slots of one machine. Fence gives the
// consumer-owned contract:
//
//   - Acquire blocks until this slot holds the fence or its context is canceled,
//   - Release drops it after the slot's active resources have closed,
//   - Held and Slot report the slot's current ownership for diagnostics.
//
// The lock is exclusive and non-expiring. It is released only when the holder
// releases it or the holding process exits: a crash releases it automatically
// because the operating system drops the lock when the file handle closes, and a
// clean stop releases it explicitly. There is deliberately no lease and no
// timeout. A paused or unhealthy holder keeps the fence until its service manager
// terminates it, so a stalled process that later resumes can never find a second
// active already running. There is no distributed election and no active-active
// path.
//
// The lock must sit on a local filesystem. A network filesystem may not drop the
// lock on process death or enforce exclusivity across hosts, which are the two
// properties the fence depends on. FencePath derives the lock path from the
// deployment identity — project, environment, site, and machine — and never from
// the slot, so both slots of a machine contend for the same lock. The runtime
// directory that holds it is a local configuration value supplied by the
// composer, never part of the deployment descriptor.
//
// # Operational identity versus domain identity
//
// Slot identity is operational only. OperationalName composes the "<machine>/<slot>"
// label that distinguishes the two slots in logs and in operational event node
// identity. Domain identities stay machine-scoped: registration proposal and
// decision IDs, and the durable handler consumer names, carry the machine and
// never the slot, so two slots on one machine can never become two domain voters.
//
// # Shutdown semantics
//
// The states and the fence imply the shutdown rules the runtime follows:
//
//   - A process crash releases the OS lock automatically; no cleanup runs.
//   - A graceful active shutdown closes the public API, the handlers, the
//     projector, and the transport, in that order, and releases the fence only
//     after they have closed. Releasing earlier could let a second slot open the
//     same listener or embedded server while this one is still up.
//   - Canceling a standby ends its fence wait without acquiring and without
//     promotion. A standby that was never active has nothing to release.
//   - A full machine shutdown stops slot B before slot A (see StopOrder), so the
//     machine leaving does not look like an active failure that the other slot
//     should promote into.
package instance
