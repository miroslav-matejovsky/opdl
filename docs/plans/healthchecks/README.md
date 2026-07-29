# Plan: distributed service health checks

Status: implemented. Every step is complete except route partition testing,
which stays open because no mechanism for it exists on a developer host. The
operator-facing result is [Service health](../../04-service-health.md); these
documents remain the record of how it was decided and what was measured.

This plan introduces platform-managed health checks for every service authored
on a machine. The Primary and Standby platform instances both probe every local
service. Each instance publishes its observations through the site's existing
NATS cluster. Every running instance keeps an in-memory view of service health
for the whole site.

These documents record the implemented contract, the decisions behind it, and
the little that remains, so there is one health-check source of truth.

## Implementation status

| Step | Status |
| --- | --- |
| 00 - decisions | Complete; the load envelope is accepted and published |
| 01 - descriptor | Complete |
| 02 - local probe engine | Complete, including deterministic startup jitter |
| 03 - NATS distribution and site reduction | Complete |
| 04 - lifecycle and API | Complete; `GET /health/services`, `serviceMonitor`, OpenAPI, SDK, and a non-vacuous .NET E2E gate |
| 05 - black-box convergence | Complete except route partition |
| 06 - production hardening | Complete except the environment's security posture |

The two open items are tracked as P1 in
[gaps](05-gaps-risks-and-improvements.md) and in
[route security](../../backlog/route-security.md). Neither blocks the feature;
both are about the environment it runs in rather than about what it does.

## Goals

- Run the authored HTTP health check for every service on the local machine.
- Run the same checks from both platform instances when a Standby is deployed.
- Distribute current observations to every platform instance at the site.
- Rebuild a useful site view after process restart or network recovery without
  persisted health results.
- Expose the local instance's site view to operators and SDK clients.
- Keep service health independent of platform Primary Ownership.
- Make loss, staleness, disagreement, and incomplete site connectivity visible.

## Non-goals

- Persisting or replaying service health history.
- Using health results to promote a platform Standby.
- Assigning service master/slave roles or automating service failover.
- Installing, starting, stopping, or restarting Windows Services.
- Replacing the existing platform `/health`, `/health/live`, `/health/ready`, or
  `/health/ha` contracts.
- Completing the durable site event journal or `eventfabric.Consumer`.
- Building the general distribution abstraction proposed in
  `docs/drafts/distribution.md`.

## Recommended design

```text
authored service blocks
        |
        v
builder resolves local probe policy and site service inventory
        |
        v
Primary Instance                     Standby Instance
local probe workers                  local probe workers
        |                                  |
        +---------- Core NATS --------------+
                         |
              every site instance subscribes
                         |
              in-memory site health view
                         |
                  GET /health/services
```

Health observations are current state snapshots. They are not immutable domain
events. They must not pass through `events/storage.Publisher`, the instance
event log, the machine event store, or a future durable site journal.

Core NATS is at-most-once and has no replay in the current deployment. Every
probe attempt therefore republishes the current stable observation. A missed
message is repaired by the next attempt. Receivers expire observations that
stop arriving. Restarted instances reconstruct remote state as fresh reports
arrive.

The achievable consistency is bounded eventual consistency. During a route
partition, instances can disagree. After connectivity returns and a new report
from each live observer is delivered, they converge on the same deterministic
reduction. Strong consistency is incompatible with the requested no-persistence
design and the current Core NATS transport.

## Documents

1. [Current state and scope](01-current-state-and-scope.md)
2. [Target architecture](02-target-architecture.md)
3. [Contract and convergence](03-contract-and-convergence.md)
4. [Configuration and API](04-configuration-and-api.md)
5. [Prioritized gaps, bugs, and improvements](05-gaps-risks-and-improvements.md)
6. [Implementation roadmap](06-implementation-roadmap.md)
7. [Testing and observability](07-testing-and-observability.md)
8. [Required decisions](08-decisions.md)

## Plan dependencies

```text
decisions [done]
    |
    v
descriptor contract [done]
    |
    +----------> local probe engine [done]
    |                    |
    v                    v
NATS health transport -> site reducer [done]
                         |
                         v
                 runtime lifecycle [done]
                          |
                          v
                 public API [done]
                         |
                         v
          scenarios [done], hardening [done]
                         |
                         v
            route partition testing [open]
```

## Relation to the hierarchy plan

The older [hierarchy plan](../hierarchy/README.md) remains untouched and remains
superseded for the reason stated in its own README: it assumes the removed
registration domain and a dynamic event-sourced topology.

Health distribution does not make that plan current and does not implement its
open Step 06. `healthfabric` carries repeated, expiring, at-most-once snapshots
with no persistence or replay. Hierarchy Step 06 asks how durable site-scoped
facts receive ordering, replay, acknowledgement, and outage recovery. The two
features share the embedded NATS cluster but have intentionally different data
and correctness contracts. Health is useful evidence for a future hierarchy
rewrite, not a durable site-event solution.

## Completion criteria

- Both instances probe all services authored on their machine in every
  ownership state.
- Every connected instance exposes the same status after receiving the same
  observations.
- A dropped observation is repaired by a later periodic publication.
- A silent observer expires without turning the target service into a false
  healthy result.
- A restarted instance needs no health-result file and converges from new NATS
  traffic.
- Target service failure never causes platform ownership transfer.
- Existing platform redundancy continues when NATS routes are unavailable.
- The implementation documents its delivery and consistency guarantees.
- `task all` passes at the end of every implementation step.

