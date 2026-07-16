# Stage 0: Remaining decisions and contract

Estimate: 2 person-days total. Decisions required for Stage 1 are resolved and
removed from this file.

## Objective

Resolve the remaining choices that affect persisted records, the public API,
SDK behavior, fabric durability, and upgrade orchestration. Record each result
before its required implementation stage begins.

## Required decisions

### 1. Registration acceptance unit

Required for: Stage 4.

Recommended approach: retain one registration vote per machine. Primary and
secondary are redundant active executors that race to write the same idempotent
machine confirmation. Acceptance requires one accepted confirmation from every
expected machine, as it does today.

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

Required for: Stages 4 and 5.

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

Required for: Stage 5.

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

### 4. Fabric data safety

Required for: Stages 2, 6, and 7.

Recommended approach: configure and test Olric so loss of one platform process
does not lose acknowledged registration state. Perform a focused spike against
the pinned Olric version to verify replica count semantics during graceful leave,
hard process loss, and join. Do not claim reliability from membership alone.

Pros:

- The redundancy claim covers state, not only listening sockets.
- It provides evidence for the rolling-upgrade procedure.

Cons:

- Olric may not guarantee placement on the paired same-machine instance.
- Membership churn is already known to weaken create-if-absent semantics.

Alternative: replace or wrap storage. This is a major scope increase and should
only be chosen if the spike proves Olric cannot survive the required failure.

Decision needed: define the minimum acknowledged-state survival guarantee. The
recommended minimum is any single platform process loss within a running site.

### 5. Upgrade owner and compatibility window

Required for: Stages 3, 4, and 6.

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
- Provide decision 4 before starting Stage 2 reliability work.
- Provide decision 5 before starting Stage 3 compatibility work.
- Provide decisions 1 and 2 before starting Stage 4.
- Provide decision 3 before starting Stage 5.
- Revise estimates if storage replacement or OS-specific supervision is chosen.

## Exit criteria

- All five remaining decisions are resolved before their required stages.
- No unresolved wire-format, retry, durability, or supervisor choice is hidden
  inside implementation work.
