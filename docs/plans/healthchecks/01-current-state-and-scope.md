# Current state and scope

Reviewed against commit `2696172` plus the fixes made during this review.

## Implemented path

The health path now runs from blueprint to every process:

```text
blueprint service health_check
        |
        v
structured local services + static site_services inventory
        |
        v
machine/servicehealth workers
        |
        +---- local stamped observation ----> site/healthview
        |
        +---- site/healthfabric ---- Core NATS ----> every peer healthview
```

| Capability | Current state |
| --- | --- |
| Authored per-service probe policy | Implemented |
| Structured local policy in descriptor | Implemented |
| Static site service inventory | Implemented |
| Runtime HTTP probe engine | Implemented |
| Consecutive failure tracking | Implemented |
| Process-lifetime Primary and Standby monitoring | Implemented |
| Versioned NATS health publication | Implemented |
| NATS subscription barrier | Implemented |
| Per-service latest-value publication buffer | Implemented |
| Complete in-memory site view | Implemented |
| Per-observer fencing and receiver-relative expiry | Implemented |
| Deterministic site reduction | Implemented |
| Service-health HTTP API | Missing |
| Generated SDK service-health API | Missing |
| Multi-instance and multi-machine convergence scenarios | Missing |

## Descriptor boundary

The descriptor carries two independent halves:

- `services` contains this machine's service identity, role, and full probe
  policy.
- `site_services` contains every service unit at the site, expected observer
  roles, and exact derived freshness. It contains no remote endpoint.

Builder, platform, and conformance validation reject malformed paths, invalid
durations, endpoint collisions, missing local inventory entries, extra local
inventory entries, inconsistent machine policy, and freshness that differs from
`2 * interval + timeout`.

## Local monitor

`platform/internal/machine/servicehealth` runs one worker per local target in
both platform roles. A worker probes immediately, then waits one interval after
the completed attempt. Attempts for one target never overlap.

The stable state starts Unknown. A success makes it Healthy and clears pending
failures. Failures retain the previous stable state until the authored retry
threshold is reached, then make it Unhealthy. One success recovers immediately.
Shutdown cancellation is not recorded as target failure.

HTTP probes use GET against the descriptor machine IP, accept 2xx, disable
redirects and environment proxies, bound body reads, and use the authored
timeout.

Deterministic observer-specific startup jitter is not implemented. The first
probe is immediate in every process, so Primary and Standby can probe together.
This is hardening work, not a correctness dependency.

## Site view and transport

`platform/internal/site/healthview` owns the in-memory picture. It is created
from static inventory, so a unit that has never reported is present as Unknown.
Each expected observer has its own slot. `(epoch, sequence)` fencing prevents an
old process or late message from replacing a newer report. Freshness is measured
from arrival on the receiver's monotonic clock and evaluated when a snapshot is
read.

`platform/internal/site/healthfabric` owns the wire contract and Core NATS
adapter. It uses the fixed subject `opdl.service_health.v1`, an 8 KiB message
bound, strict identity and numeric checks, and a dedicated in-process
connection. The sender retains only the newest pending observation per local
service. Publication never waits for route recovery.

The NATS connection disables echo. The sender applies its stamped observation
directly to its own view, while other connections receive it through NATS. This
avoids racing normal self-delivery against the direct apply.

## Runtime lifecycle

`internal/app` builds the view, opens the health connection, establishes and
flushes the subscription, opens the publisher, and starts probing before
entering ownership management. Shutdown stops those components in reverse.

The observation epoch is the durable instance epoch captured immediately after
the process-start advance. It stays fixed in the publisher across ownership
changes. A later process starts with a greater durable epoch.

Failure to start service monitoring records
`platform.app.service_health_start_failed` and stops the process. Target
failure, route loss after startup, missing observers, and stale reports are
observed state and do not affect platform readiness or Primary Ownership.

## Remaining gap

The view has no reader. `serviceHealth.view` is maintained but intentionally has
no exported accessor until `GET /health/services` is composed. Without that
endpoint, black-box tests cannot assert cross-instance convergence, expiry, or
route recovery even though probes, publication, subscription, and reduction are
running.

The .NET E2E project also discovers zero tests while `dotnet test` exits
successfully. Step 04 must make zero discovered tests fail before the generated
health client is treated as covered.

## Scope constraints

- Windows only.
- Monitoring runs in Primary and Standby, Active and Passive.
- Local redundancy is independent of NATS route and target health.
- No health-result persistence, JetStream, or durable consumer.
- No service lifecycle control.
- No use of the durable `eventfabric.Consumer` contract.
- New API behavior requires OpenAPI, SDK, unit, integration, and black-box
  coverage.
