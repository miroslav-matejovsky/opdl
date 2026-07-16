# Stage 0: Remaining decisions and contract

Estimate: 2 person-days total.

Complexity: medium.

Decisions required for Stage 1 are resolved and removed from this file.

## Objective

Resolve the remaining choices that affect persisted records, the public API,
SDK behavior, and upgrade orchestration. Record each result before its required
implementation stage begins.

## Required decisions

### 1. Registration acceptance unit

Required for: Stage 5.

Recommended approach: retain one registration vote per machine. Primary and
secondary are redundant active executors that may each write the same semantic
machine confirmation to their own store. Deterministic fact identity collapses
the copies into one vote. Acceptance requires one accepted confirmation from
every expected machine, as it does today.

Pros:

- Registration continues while either process is down or being upgraded.
- A retry from primary to secondary remains the same proposal and origin.
- It preserves the original topology rule that every machine participates.
- Consumers do not need to reason about platform process health.

Cons:

- Acceptance does not prove both processes examined a proposal.
- Mixed-version correctness depends on the rolling compatibility contract.

Alternative: require a confirmation from every configured platform instance.
This proves both participated, but every new registration stays pending whenever
one instance is down. That defeats failover during maintenance and is not
recommended.

### 2. Public progress model

Required for: Stages 5 and 6.

Recommended approach: rename the existing machine-level
`platform_instances` response field and type to `platform_machines` and
`PlatformMachineRegistrationStatus`. Do not expose primary/secondary progress in
the registration contract. Add instance identity to operational events instead.

Pros:

- The public API says what the acceptance algorithm actually waits for.
- The SDK hides redundancy as required.
- Service roles and platform instance labels stay separate.

Cons:

- This is a breaking generated API change.
- Existing API examples and tests need updates.

Alternative: keep the old JSON name while documenting machine grouping. This
reduces churn but leaves a misleading public contract.

### 3. SDK routing and retry boundary

Required for: Stage 6.

Recommended approach: round-robin the first attempt across primary and
secondary, then retry once on the other endpoint for connection failure,
non-caller timeout, HTTP 408, 502, 503, or 504. Do not retry 4xx domain results,
HTTP 500, caller cancellation, or non-replayable content. Buffer the small JSON
request body before the first attempt.

Pros:

- Both instances are used in normal operation.
- Failover is bounded and predictable.
- Domain conflicts and validation failures are not hidden.

Cons:

- One retry can increase tail latency.
- The precise transport implementation needs careful request cloning and
  disposal tests.

Alternative: primary-preferred traffic with secondary only on failure. This is
simpler but is active-passive from the consumer path and does not exercise the
secondary continuously.

### 4. Acknowledged-state data safety

Required for: Stages 3, 7, and 8.

Decision: accepted. Use two owner-local durable registration stores per
redundant machine. Primary writes only the primary store. Secondary writes only
the secondary store. Each instance can read its sibling's store but cannot
write through the storage API exposed for that sibling. The initial backend is
file-based behind a registration-owned abstraction so SQLite or another local
backend can replace it later.

Add an OPDL-owned primary/secondary synchronization loop. Each process pulls
immutable facts from the sibling's read-only store and idempotently writes
missing facts to its own writable store. Use deterministic fact identifiers and
a full scan initially; add cursors only if measured data volume requires them.
Synchronization runs at startup and periodically, and can be invoked during
readiness and upgrade checks where a bounded catch-up is required.

Do not use Olric subscriptions, events, or relays to synchronize the paired
local stores. Olric remains the transient site-wide exchange layer between
machines. It is not the durability authority. Before a successful operation
exposes a registration fact, the creating instance must durably write that fact
to its own local store. A surviving sibling can read facts not yet copied and
complete local synchronization after graceful stop, hard process loss, restart,
join, or rolling replacement.

Use immutable, deterministically identified facts as the recovery source.
Derive mutable current views from those facts. Define deterministic merge rules
for the case where both active instances create the same semantic machine fact.
Do not rely on cross-store create-if-absent or on Olric membership for global
uniqueness.

Minimum guarantee: loss of any single platform process within a running site
does not lose registration state already acknowledged through the API. The
guarantee assumes the machine and its persistent filesystem remain available
to the sibling. Acknowledged means a successful POST has durably retained its
proposal and an accepted status has durable facts sufficient to reconstruct
that status.

Pros:

- Process-local writes have one clear owner and need no cross-process write
  lock.
- Direct synchronization normally gives both stores a copy without coupling
  local redundancy to Olric behavior.
- Hard process loss does not remove either file the sibling reads.
- Olric replica placement is no longer part of the acknowledged-state claim.
- A small storage abstraction allows a later SQLite or other backend without
  changing registration semantics.

Cons:

- Synchronization is asynchronous, so the stores are not guaranteed to contain
  two copies of a newly acknowledged fact immediately. Disk or machine loss can
  still lose both availability and state.
- Same-machine read access and durable file replacement or flushing require
  focused Windows and Linux tests.
- Both writers can create the same semantic fact, so identity, merge, and
  projection rules must be deterministic.
- The synchronization loop needs deterministic convergence, bounded readiness
  behavior, failure reporting, and cross-platform concurrent file tests.
- Read-only sibling access is enforced by application capabilities. OS-level
  enforcement requires distinct service accounts and filesystem permissions.
- Local synchronization does not distribute state to other machines. Site-wide
  exchange remains a separate fabric responsibility.

Rejected approach: claim durability from Olric replica count and membership.
Replica placement is not guaranteed to target the same-machine sibling, and
membership churn already weakens create-if-absent behavior.

Deferred alternative: replace the file backend with SQLite or another local
store if file durability, concurrent read behavior, or operational tooling is
insufficient. Replacing the site-wide distribution mechanism is a separate
scope increase and is not required by this decision.

### 5. Upgrade owner and compatibility window

Required for: Stages 4, 5, and 7.

Recommended approach: OPDL packages expose two launch specifications,
readiness, and a documented rolling sequence. An external service manager owns
process restart. Support N and N+1 running together for public HTTP, fabric
membership, and registration records during one rolling upgrade.

Pros:

- It is portable across Windows and Linux.
- The runtime and package contracts are testable in this repository.
- The required compatibility window is explicit.

Cons:

- OPDL alone does not install or supervise services.
- A production integration must implement the sequence correctly.

Alternative: add OS-specific supervisors/installers to this effort. This gives
more end-to-end control but materially expands scope and estimates.

Decision needed: identify the production supervisor and whether installer work
belongs in this repository.

## Next steps

- Stage 1 is complete.
- Stages 2 and 3 may start. Decision 4 is resolved.
- Provide decision 5 before starting Stage 4 compatibility work.
- Provide decisions 1 and 2 before starting Stage 5.
- Provide decision 3 before starting Stage 6.
- Revise estimates if storage replacement or OS-specific supervision is chosen.

## Exit criteria

- All unresolved decisions are resolved before their required stages.
- No unresolved wire-format, retry, durability, or supervisor choice is hidden
  inside implementation work.
