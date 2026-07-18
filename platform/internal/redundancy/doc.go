// Package redundancy is the platform's local warm-standby mechanism: the stable
// process identities (slots) a machine runs, the lifecycle states they move
// through, the machine fence that decides which one serves, and the local status
// a deployer reads to see what each slot is doing.
//
// It owns terminology and mechanism, not composition. It does not open a
// transport, run a projector, or bind a listener. The app package consumes this
// package to run one slot as either the active runtime or a projection-only warm
// standby; a domain package never sees it.
//
// # Machine and slot
//
// A machine is the compiled deployment identity and one registration voter. A
// slot is a stable local process identity on that machine, either A or B. A slot
// is process identity only: never a machine, a service, a registration voter, or
// a permanent primary. Which slot is active can change across restarts, and
// running a second slot never adds a second machine decision.
//
// The two slots are symmetric. The first healthy slot to acquire the machine
// fence becomes active, so recovery never waits for one particular slot.
//
// # Lifecycle states
//
// A slot moves through the states named by State: starting, standby, activating,
// active, stopping, and failed. Only the active state owns the machine's
// externally visible, decision-producing capabilities. State enumerates the legal
// transitions; the runtime drives them and reports the current state through the
// slot's status file.
//
// # Ownership rule
//
// Exactly one process on a machine holds the fence, and only the slot holding it
// may bind the public API, run the durable domain handlers, publish machine
// lifecycle readiness, or host the embedded NATS server and its storage. A warm
// standby holds none of these: it opens a client-only transport and runs
// projectors only, so it stays caught up without producing a decision or exposing
// a second listener.
//
// # The fence
//
// The fence is an exclusive OS file lock in the machine's local runtime
// directory, shared only by the two slots of one machine. It is exclusive and
// non-expiring: released only when the holder releases it or the holding process
// exits. A crash releases it automatically, because the operating system drops
// the lock when the file handle closes. There is no lease and no timeout, so a
// paused holder keeps the fence until its service manager terminates it and can
// never wake into a second active. There is no distributed election and no
// active-active path.
//
// TryAcquire takes the fence without blocking, which is how a starting slot
// decides its role: the winner is active, and a slot that finds the fence held is
// a warm standby. Acquire blocks until the fence is free, which a standby uses to
// wait for promotion. The lock must sit on a local filesystem; a network
// filesystem may not drop it on process death or enforce exclusivity across
// hosts.
//
// FencePath and StatusPath derive their paths from the deployment identity —
// project, environment, site, and machine — under a configured local runtime
// directory. The fence path never includes the slot, so both slots contend for
// one lock; the status path is per slot, so each slot reports its own state. The
// runtime directory is a local configuration value supplied by the composer,
// never part of the deployment descriptor.
//
// # Status and lag
//
// Status is a slot's live snapshot — its slot, state, PID, applied and journal
// high-water sequences, projection lag, and last error — written atomically to
// the slot's status file for deployment diagnostics. It is not coordination
// state: the OS fence remains authoritative, and a status file can be stale after
// a crash, so it must never be treated as a fence.
//
// LagState tracks how long a slot's projection has continuously been behind the
// journal. A projection that lags beyond a configured bound is not promotable,
// and an active slot that lags beyond it stops serving rather than answering from
// a stale view.
//
// # Operational identity versus domain identity
//
// Slot identity is operational only. OperationalName composes the
// "<machine>/<slot>" label that distinguishes the two slots in logs and
// diagnostics. Domain identities stay machine-scoped: registration proposal and
// decision IDs, and the durable handler consumer names, carry the machine and
// never the slot, so two slots on one machine can never become two domain voters.
//
// # Shutdown semantics
//
//   - A process crash releases the OS lock automatically; no cleanup runs.
//   - A graceful active shutdown closes the public API, the handlers, the
//     projector, and the transport, then releases the fence only after they have
//     closed. Releasing earlier could let a second slot open the same listener or
//     embedded server while this one is still up.
//   - Canceling a standby ends its projector and any fence wait without promotion.
//   - A full machine shutdown stops slot B before slot A (see StopOrder), so the
//     machine leaving does not look like an active failure the other slot should
//     promote into.
package redundancy
