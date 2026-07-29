# Prioritized gaps, bugs, and improvements

This file is the only health-check work inventory. There is no separate health
backlog while this plan is active.

Priorities:

- P0 blocks useful delivery or production safety.
- P1 is required for operational confidence and completion.
- P2 is optional or should be driven by measured need.

## P0

| Type | Gap or risk | Impact | Required action |
| --- | --- | --- | --- |
| Delivery gap | No `GET /health/services` endpoint, response types, OpenAPI contract, or generated .NET client | The converged in-memory view cannot be consumed or asserted outside the process | Implement the Step 04 API slice on Active, Passive, and journal-less handlers |

## P1

| Type | Gap or risk | Impact | Required action |
| --- | --- | --- | --- |
| Test gate bug | The .NET E2E task succeeds when zero tests are discovered | Generated-client regressions can pass the repository gate | Fix discovery and make zero discovered tests fail before adding health API E2E coverage |
| Scenario gap | Existing scenarios cannot query health convergence | Cross-instance convergence, restart reconstruction, and recovery are not proven end to end | Add controlled services and API assertions in Step 05 |
| Route gap | Tests cover two health connections on one embedded broker, not a routed multi-broker cluster | Route partition and reconnection behavior remain inferred from Core NATS semantics | Add route interruption and recovery tests and Windows scenarios |
| Observability gap | Publisher, subscriber, stale-observer, and rejection state is not exposed through the platform health/API surface | Operators cannot distinguish target failure, monitor failure, and distribution failure | Add bounded counters and subsystem state to the API without folding target health into ownership readiness |
| Load risk | Queue, view, and message bounds are unit-tested but not measured at supported site size and minimum interval | Production resource bounds are not demonstrated | Define limits, load-test them, and publish the supported envelope |
| Scheduling risk | Primary and Standby probe immediately and can remain synchronized | Services receive duplicate probe bursts | Add deterministic observer-specific startup jitter during Step 06 hardening |
| Documentation gap | Operational response for Unknown, Degraded, stale observers, invalid messages, and route loss is not documented | Operators may misdiagnose expected transient states | Add the Step 06 runbook |

## P2

| Type | Improvement or open decision | Impact | Recommendation |
| --- | --- | --- | --- |
| Policy tuning | `fresh_for = 2 * interval + timeout` has not been validated with production timing data | Slow or highly jittery environments may expire too early | Keep the implemented formula until measurements justify a contract change |
| Probe support | Only plain HTTP is implemented | HTTPS, TCP, Windows Service state, and response-body contracts are unsupported | Add probe types only for an accepted requirement; decide certificate sourcing before HTTPS |
| Warm-up | There is no request/reply snapshot exchange | A restarted receiver waits for new periodic reports | Keep periodic repair unless measured startup latency is unacceptable |
| Identity | Site, machine, and service are the service-unit key | Multiple runtime instances of one authored service cannot be represented independently | Add an instance identifier only when the deployment model supports such instances |
| Recovery policy | One success immediately recovers a target | A flapping endpoint can switch to Healthy after one success | Keep the simple rule until a real hysteresis requirement exists |
| Audit history | Health transitions are not durable events | There is no historical health timeline | Keep out of scope unless a separate audit requirement is accepted |

## Resolved by the implementation

The current implementation resolves the original descriptor, inventory, probe,
distribution, reducer, lifecycle, validation, and bounded-buffer gaps. In
particular:

- descriptors retain full local probe policy and static site inventory;
- both Primary and Standby monitor for the process lifetime;
- the HTTP prober has bounded body handling, no proxy, no redirects, and
  cancellation;
- the publisher holds only the latest pending observation per local service and
  counts supersessions;
- inbound messages are bounded and validated against deployment scope and
  inventory;
- static expected-observer slots, `(epoch, sequence)` fencing, receiver-relative
  freshness, and deterministic reduction are implemented; and
- startup and shutdown order subscribe before probing and reverse that order on
  shutdown.

The 2026-07-29 review also fixed these implementation defects:

| Defect | Impact | Fix and regression coverage |
| --- | --- | --- |
| A publisher could receive its own NATS message before direct local apply | The direct apply could be reported as a duplicate and produce a false warning | Use a no-echo health connection; an embedded NATS test proves peer delivery without sender echo |
| `latency_ms` was converted to `time.Duration` before range validation | Extreme input could overflow and bypass the intended bound | Validate raw milliseconds before conversion; test `math.MaxInt64` |
| Zero sequence and zero check timestamp were accepted | Invalid observations could enter ordering and diagnostics | Reject both as impossible; add decode cases |
| A flush failure after subscription leaked the NATS subscription | Failed startup could retain a live callback until connection close | Unsubscribe on flush failure; assert cleanup |
| The view retained caller-owned observer-role slices and allowed duplicates | External mutation or duplicate slots could corrupt inventory invariants | Clone input and reject duplicate roles; add regression tests |

## Constraints that remain intentional

- Health uses Core NATS at-most-once snapshots. It has no result persistence,
  replay, acknowledgement, or JetStream.
- A fresh Unknown observation contributes no verdict. It remains visible in
  observer diagnostics. Healthy plus Unknown reduces to Healthy.
- Missing or stale observers do not force Unknown when another observer has a
  fresh verdict.
- Target service status does not change platform ownership, `/health/ready`, or
  the peer promotion gate.
- Health distribution is separate from durable site-event distribution in the
  hierarchy plan.
