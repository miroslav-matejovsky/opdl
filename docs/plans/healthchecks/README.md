# Plan: distributed service health checks

Status: draft for review, 2026-07-28.

This plan introduces platform-managed health checks for every service authored
on a machine. The Primary and Standby platform instances both probe every local
service. Each instance publishes its observations through the site's existing
NATS cluster. Every running instance keeps an in-memory view of service health
for the whole site.

This directory contains analysis and planning only. It does not authorize or
include production code changes.

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
decisions
    |
    v
descriptor contract
    |
    +----------> local probe engine
    |                    |
    v                    v
NATS health transport -> site reducer
                         |
                         v
                 runtime lifecycle and API
                         |
                         v
              scenarios, security, rollout
```

The descriptor contract comes first. The runtime currently receives only
service names, so no later step can run a probe or construct a complete site
view until that loss of information is fixed.

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

