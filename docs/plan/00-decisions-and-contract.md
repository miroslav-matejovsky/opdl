# Stage 0: Decisions and contract

Estimate: 2 person-days.

## Objective

Resolve the choices that affect persisted records, deployment JSON, public API
shape, SDK behavior, and upgrade orchestration. Record the results before code
changes begin.

## Required decisions

### 1. Authored configuration location

Required for: Stage 1.

Recommended approach: add an optional machine-level `platform` block with
`secondary_enabled`. Omission means `true`.

Example direction:

```hcl
machine "integration" {
  role     = "integration-server"
  ip       = "10.0.2.12"
  services = ["integration-services"]

  platform {
    secondary_enabled = false
  }
}
```

Pros:

- The exception is local to the machine whose resources are constrained.
- The default directly implements the requested safe behavior.
- It removes redundancy from project feature licensing and capability concepts.

Cons:

- HCL decoding must distinguish an omitted value from explicit `false`.
- A new nested block adds validation and fixtures.

Alternative: a direct `secondary_enabled` machine attribute. It is smaller, but
it mixes platform runtime controls with machine identity and hosted services.

Decision needed: confirm the block name and authored field name.

### 2. Resolved descriptor shape

Required for: Stages 1 and 2.

Recommended approach: the embedded descriptor carries an ordered
`platform_instances` list. It contains exactly `primary`, then optional
`secondary`. Each entry has explicit API, fabric client, and fabric memberlist
addresses. Fabric peers also identify their instance and explicit endpoints.
The list length is the resolved enablement state, so no redundant boolean is
needed in the embedded descriptor.

Pros:

- The runtime consumes concrete deployment facts rather than defaults or a
  feature flag.
- Validation can reject collisions before a package is built.
- The model cannot accidentally grow a third instance without a contract
  change.

Cons:

- Endpoint data is repeated in every machine's resolved site view.
- Builder and platform descriptor types must change together.

Alternative: carry base ports and derive all addresses in the runtime. This is
smaller but conflicts with the requirement that addresses and ports be defined
in the deployment descriptor and makes conformance harder to inspect.

Decision needed: approve exact JSON field names before implementation.

### 3. Default ports

Required for: Stages 1 and 2.

Recommended initial allocation:

| Instance | HTTP API | Olric client | Olric memberlist |
| --- | ---: | ---: | ---: |
| Primary | 8080 | 3320 | 3322 |
| Secondary | 8081 | 3321 | 3323 |

The builder joins these ports to each machine IP and writes full host:port
addresses into the resolved descriptor.

Pros:

- Primary preserves all existing defaults.
- Secondary uses adjacent, predictable ports.
- A descriptor is self-contained and readable.

Cons:

- These ports must be reserved and checked against hosted service allocations.
- Adjacent port allocation is a convention that must remain stable.

Decision needed: confirm the port allocation and IPv4/IPv6 expectations.

### 4. Runtime override meaning

Required for: Stages 1 and 3.

Recommended approach: interpret "per service" as per platform instance. Use
`instances.primary` and `instances.secondary` TOML sections for socket
overrides. Do not use hosted service names as configuration keys.

Pros:

- It avoids confusing platform processes with hosted services.
- One shared configuration file can describe both process launches.
- Overrides are validated as a complete set before either launch.

Cons:

- Existing top-level `address` and `[fabric.olric]` settings move.
- Operators must name the selected instance even when secondary is disabled.

Alternative: one TOML file per process retaining unkeyed settings. This is
simple in the runtime but duplicates common settings and makes package launch
metadata harder to audit.

Decision needed: confirm whether "service" meant platform instance. If it meant
each hosted application service, stop and define that additional scope before
Stage 1.

### 5. Process model and selector

Required for: Stages 1, 3, and 6.

Recommended approach: run the same machine-specific binary as two OS processes,
with an explicit `--instance primary|secondary` selector. Reject `secondary`
when it is absent from the embedded descriptor. Do not allow a custom name.

Pros:

- One process can be replaced while the other stays live.
- Process crash isolation is real.
- The package still contains one machine-specific binary.

Cons:

- A supervisor or deployment system must own two process lifecycles.
- Logs, event files, working directories, and configuration must be safe for
  concurrent processes.

Alternative: one process hosts both instances. It reduces deployment work but
does not meet process-failure redundancy or zero-downtime binary upgrades.

Decision needed: confirm explicit CLI selection and two-process ownership.

### 6. Registration acceptance unit

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

Decision needed: this is blocking for Stage 4.

### 7. Public progress model

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

Decision needed: approve the breaking rename. The repository is currently in an
experimentation phase, so the clean contract is recommended.

### 8. SDK routing and retry boundary

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

Decision needed: confirm the transient status list and one-retry limit.

### 9. Fabric data safety

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

### 10. Upgrade owner and compatibility window

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

## Deliverables

- A short decision record for each item above, with final names and wire shapes.
- One worked descriptor JSON example for a redundant machine.
- One worked descriptor JSON example for a primary-only machine.
- One worked TOML example showing a secondary socket override.
- A failure matrix covering primary crash, secondary crash, whole-machine loss,
  network partition, and rolling replacement.
- A compatibility statement defining which N/N+1 combinations are supported.

## Exit criteria

- All ten decisions are resolved or explicitly deferred with an owner and date.
- No blocking schema, retry, voting, or supervisor question remains for Stage 1.
- The overall estimate is revised if OS-specific supervision or storage
  replacement is selected.
