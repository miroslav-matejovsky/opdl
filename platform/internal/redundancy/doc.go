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
// Only the owner may serve domain operations, run durable handlers, or publish
// lifecycle readiness. Event Fabric membership is independent of ownership: an
// instance selected for storage runs its authored NATS server in either state.
// The Passive instance runs only its projector and answers for itself on its own
// API address.
//
// # What this package decides, and what it does not
//
// This package owns the ownership lifecycle: which of a machine's two instances
// may run its active composition, when the other must have stopped, and what an
// operator is told about the move. It does not know what a composition is. The
// runtime supplies two functions — one to run while Passive, one to run while
// Active — and Contend decides when each runs. See ownership.go.
//
// The sequencing is the point. The Passive and Active compositions of one
// instance use the same Event Fabric identity and storage. Nothing in the type
// system prevents them from opening it concurrently. Contend does: Passive has
// returned before Active is called, and ownership is released only after Active
// has returned.
//
// # Primary Ownership
//
// Ownership is a finite, renewable lease recorded in a machine-wide file whose
// path and timings are authored in the machine's blueprint and carried in its
// deployment descriptor. The owner renews it periodically; a Standby takes it
// over once it lapses and the peer's health endpoint reports the owner can no
// longer serve. It is released cleanly after active resources close, or left to
// lapse when the process dies. See lease.go for the record and its operations,
// and ownership.go for the promotion machine.
//
// The lease replaced a non-expiring Windows named mutex. The mutex gave mutual
// exclusion by construction but could never fail over from an unresponsive-but-
// alive holder: a hung process kept its ownership until its service manager
// killed it. The lease trades the kernel guarantee for a bounded failover — a
// hung owner stops renewing and loses ownership on expiry — and keeps split-brain
// out by three combined means: the two instances share one host's clock, so an
// expiry means the same instant to both; an owner that cannot renew steps down
// before its lease could lapse from a promoter's view; and a promoter takes over
// only when the peer is also unhealthy. Under the Preferred Primary policy an
// Active Standby also hands ownership back once the Primary has been healthy for a
// stabilization window. See docs/plans for the finalization plan and what
// remains.
//
// # Per-instance endpoints and transfer ordering
//
// Each instance owns its own endpoints. Every address either of them binds is
// resolved onto that instance's record in the deployment descriptor, and nothing
// on a machine is shared between them except the lease file, which is not a
// port.
//
// An instance's API address is bound for its whole lifetime rather than only
// while it is Active, so a transfer changes which address serves domain
// operations rather than moving one address between processes:
//
//	both instances listen and maintain their own Event Fabric membership
//	the active instance closes domain serving and its Event Fabric composition
//	the active instance releases ownership
//	the waiter acquires ownership and closes its Passive composition
//	the waiter opens its Active composition over the same authored membership
//	the waiter catches up and serves domain operations on its existing API address
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
// # Where an instance's state is read
//
// Nowhere in this package. There is no status file and no state to poll: every
// transition is stated as an event by whichever component makes it, and an
// instance's role and PID travel on each event's origin. This package states the
// ownership half; runtime composition states the composition half and, once a
// second, watches its projection and states each change in whether the instance
// is current enough to be handed the machine.
//
// The file this replaced was a live snapshot rewritten once a second, and every
// field on it is now in the record: role and PID from the origin, lifecycle state
// from the transition events, and progress, lag, and failover readiness from
// platform.app.failover_readiness_changed. What it could do that the record
// cannot is answer "is this still true?" from its own timestamp. A reader that
// needs that asks the instance's own API, which is bound in every state, rather
// than reading a file that a dead process leaves behind unchanged.
//
// # Events
//
// This package states its own facts, declared in events.go: the object it
// opened, that it is waiting, that it took ownership and whether the previous
// owner handed it over or died, and how each activation ended. The package that
// runs the transition is the one that can report it accurately, so it states
// them itself rather than returning them for a caller to describe.
//
// Contend is handed the events.Publisher it states them through, so this package
// never reaches for one and never chooses where they are stored. Runtime
// composition gives it the process-local publisher, whose only backend is the
// local JSONL record: a machine contends for ownership before it has a journal to
// write to, and an instance that never becomes active never gets one at all.
//
// Every statement here is on the startup path, where Contend can return an
// error, so a failure to state one stops the instance. Ownership that moved with
// no record that it did is not a state an operator can be asked to reason about.
package redundancy
