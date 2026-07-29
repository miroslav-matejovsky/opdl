# Implementation roadmap

Steps 00 through 03 are complete except for production security and startup
jitter, which are tracked in Step 06. Step 04 lifecycle composition is complete.
The next implementation slice is the Step 04 public API.

## Step 00: accept semantics and security boundary

Status: complete for feature semantics. D13 route security remains an open
production gate in Step 06.

| | |
| --- | --- |
| Complexity | Medium |
| Estimate | 1-2 person-days |
| Depends on | Nothing |

Actions:

1. Accept or amend every decision in
   [Required decisions](08-decisions.md).
2. Write the final observation schema and reducer examples before package APIs.
3. Decide whether NATS route security is part of the first production milestone
   or an explicit trusted-network prerequisite.
4. Define the bounded eventual consistency statement as a public contract.
5. Define service counts and minimum intervals used for load bounds.

Acceptance:

- No required decision remains implicit.
- Delivery, ordering, retry, recovery, freshness, and disagreement behavior are
  testable statements.
- The rollout environment's security assumption is explicit.

## Step 01: carry health policy and site inventory

Status: complete.

| | |
| --- | --- |
| Complexity | Medium |
| Estimate | 2-4 person-days |
| Depends on | Step 00 |

Actions:

1. Add structured `Service` and `HealthCheck` types to builder deployment and
   platform config contracts.
2. Resolve complete local probe policies instead of `ServiceNames()`.
3. Resolve static site health inventory into each machine descriptor.
4. Tighten HTTP path validation.
5. Validate site inventory, expected observer roles, and derived freshness.
6. Update descriptor summaries to show concise local probe policy and site unit
   count.
7. Update all builder, config, embedded descriptor, conformance, example, and
   packaging tests.
8. Update architecture documentation beside the descriptor code.

Acceptance:

- A built descriptor contains every value needed for local probes.
- Every descriptor at one site contains the same ordered health inventory.
- Remote inventory contains no remote probe endpoint details.
- Malformed or incomplete health JSON fails runtime load.
- `task all` passes.

## Step 02: implement the local probe engine

Status: complete for functional behavior. Deterministic startup jitter is
deferred to Step 06 hardening.

| | |
| --- | --- |
| Complexity | Medium |
| Estimate | 3-5 person-days |
| Depends on | Step 01 |

Actions:

1. Create the machine-level package and package documentation.
2. Define typed targets, controlled failure categories, observations, probe
   interface, clock/scheduler seams, and monitor lifecycle.
3. Implement the HTTP probe with reusable transport, disabled proxy and
   redirects, bounded body handling, and request cancellation.
4. Implement startup Unknown, consecutive failure threshold, immediate success
   recovery, per-attempt publication, and non-overlapping schedules.
5. Add deterministic bounded startup jitter in Step 06.
6. Add table-driven unit tests with `httptest` and injected time. Use
   `t.Context()` and no sleeps.
7. Add race-focused shutdown and concurrent target tests.
8. Add package documentation explaining invariants.

Acceptance:

- Each configured target is probed independently.
- Retry and recovery behavior matches the accepted state table.
- Cancellation stops requests and workers promptly.
- No target result can crash or block another target indefinitely.
- `task all` passes.

## Step 03: implement ephemeral NATS distribution and site reduction

Status: complete for the transport, wire contract, buffering, reducer, and
composition dependency. Tests include fake connections and two real health
connections on one embedded NATS broker. Routed multi-broker behavior belongs
to Step 05.

| | |
| --- | --- |
| Complexity | High |
| Estimate | 4-7 person-days |
| Depends on | Steps 01 and 02 |

Actions:

1. Create the site-level package and package documentation.
2. Implement the versioned observation JSON contract and strict validation.
3. Add a NATS adapter using an in-process connection and fixed health subject.
4. Subscribe and flush before publishing the first local observation.
5. Add bounded per-key latest-value publication buffering and error counters.
6. Build the complete in-memory view from static inventory.
7. Apply epoch and sequence ordering, duplicate rejection, freshness expiry,
   and deterministic reduction.
8. Produce immutable, deterministic query snapshots.
9. Add fake-transport contract tests.
10. Add embedded NATS coverage. One-server peer delivery and no-echo are
    complete; routed instances, loss repair, restart warm-up, and route recovery
    remain Step 05 work.
11. Update architecture dependency rules.

Acceptance:

- Applying the same newest observer snapshots in any order yields the same view.
- Missing, duplicate, and old messages have specified outcomes.
- One dropped publication is repaired by a later full snapshot.
- Restart uses no result file and converges from new traffic.
- A publish outage does not stop local probing or local ownership.
- `task all` passes.

## Step 04: compose both instances and add the API

Status: lifecycle composition is complete. API, platform subsystem status,
OpenAPI, generated SDK, and .NET test discovery remain.

| | |
| --- | --- |
| Complexity | High |
| Estimate | 3-5 person-days |
| Depends on | Steps 02 and 03 |

Actions:

1. Capture the advanced durable instance epoch in `app.process` and keep it
   fixed for the health publisher lifetime.
2. Adapt local descriptor services into typed targets.
3. Build the site view, subscribe and flush, then start monitoring in `app.Run`.
4. Keep monitors alive through ownership transitions.
5. Stop monitoring before distribution and close the view in reverse order.
6. Add `GET /health/services` to all Active, Passive, and journal-less handlers.
7. Add API response types, deterministic sorting, status summaries, observer
   details, freshness, and distribution state.
8. Rename or redefine the hardcoded `internalServices` platform check.
9. Prove target failure does not change `/health` to Unhealthy or trigger
   promotion.
10. Fix .NET E2E test discovery and make zero discovered tests fail the gate.
11. Regenerate OpenAPI and .NET SDK artifacts.

Acceptance:

- Primary and Standby both probe all local services while running.
- Ownership transfer does not interrupt or duplicate a worker inside one
  process.
- Both instance endpoints expose their own current site view.
- Failed targets do not fail platform readiness or local redundancy.
- API conformance and discovered SDK E2E tests pass.
- `task all` passes.

## Step 05: add black-box convergence and failure scenarios

Status: pending the public API.

| | |
| --- | --- |
| Complexity | High |
| Estimate | 3-6 person-days |
| Depends on | Step 04 |

Actions:

1. Extend the Windows scenario harness with controllable fake HTTP services
   bound to each simulated machine IP.
2. Add primary-only local monitoring.
3. Add Primary and Standby duplicate observation coverage.
4. Add two-machine site convergence.
5. Change a target from Healthy to Unhealthy and back.
6. Kill and restart one platform instance and prove reconstruction without a
   health file.
7. Interrupt a NATS route, prove local checks continue and views expose
   staleness, then prove convergence after recovery.
8. Transfer Primary Ownership while checks and NATS traffic continue.
9. Assert bounded polling conditions instead of sleeping.

Acceptance:

- Every running instance reaches the same expected view after connectivity and
  fresh reports.
- The documented temporary divergence is observable during a partition.
- No health-result persistence file is created.
- Existing redundancy scenarios remain unchanged in meaning.
- `task all` passes.

## Step 06: production hardening and rollout

Status: pending.

| | |
| --- | --- |
| Complexity | Medium to high |
| Estimate | 2-5 person-days, excluding certificate infrastructure |
| Depends on | Steps 00 and 05 |

Actions:

1. Complete the accepted NATS route security work or enforce the documented
   trusted-network restriction.
2. Load-test the accepted maximum service count at minimum intervals with two
   platform instances per machine.
3. Verify queue bounds, memory bounds, connection recovery, and shutdown times.
4. Add deterministic observer-specific startup jitter and verify its bound.
5. Add operational runbooks for Unknown, Degraded, stale observers, route
   partitions, and invalid messages.
6. Update root architecture, event, platform, machine, site, and instance docs.
7. Add upgrade notes for the incompatible structured descriptor change.

Acceptance:

- Security posture matches the target environment.
- Resource use remains bounded under target failure and NATS outage.
- Operators can distinguish target failure from monitor failure and route
  partition.
- All repository documentation describes the implemented behavior.
- `task all` passes.

## Remaining delivery order

1. Complete Step 04 API, subsystem health, generated artifacts, and .NET test
   discovery.
2. Complete Step 05 convergence, restart, partition, and recovery scenarios.
3. Complete Step 06 security, jitter, load validation, and operations work.

The largest uncertainty remains realistic multi-machine route partition testing
on one Windows scenario host.
