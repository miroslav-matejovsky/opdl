# Required decisions

Every decision below is proposed, not accepted. Accept or amend them before
implementation. Decisions D01 through D12 block the core design. D13 blocks
production rollout. D14 and D15 bound the scope.

## Decision summary

| ID | Decision | Recommendation | Rationale |
| --- | --- | --- | --- |
| D01 | Consistency guarantee | Bounded eventual consistency | Strong consistency conflicts with Core NATS, partitions, and no persistence |
| D02 | Health data category | Ephemeral state snapshot, not event | Results need replacement and expiry, not replay or audit |
| D03 | Transport | Dedicated Core NATS subject | Existing cluster supports live fan-out without JetStream |
| D04 | Monitor lifetime | Whole process, both instance roles | Required observers must run in Active and Passive states |
| D05 | Target identity | Site, machine, service | Service names are only unique within a machine |
| D06 | Observer identity | Target plus fixed platform role and process epoch | Prevents Primary and Standby from overwriting each other and fences restart |
| D07 | Publication cadence | Publish after every attempt | Repairs at-most-once loss and restart without persistence |
| D08 | Freshness | `2 * interval + timeout`, receiver-relative | Tolerates one missed report without trusting wall clocks |
| D09 | Aggregation | Healthy, Unhealthy, Degraded, Unknown reducer | Separates disagreement and absence from target failure |
| D10 | HTTP semantics | GET to machine IP, 2xx success, no redirect or proxy | Simple, deterministic, and works with simulated Windows machines |
| D11 | Public query | `GET /health/services` on every instance | Every process owns a complete in-memory view |
| D12 | Redundancy coupling | No effect on platform ownership or readiness | Ownership movement cannot repair the same machine service |
| D13 | Route security | Mutual route security before production | Cluster name alone does not protect health integrity |
| D14 | Distribution abstraction | Narrow health interfaces only | Avoids blocking a concrete feature on an unaccepted general refactor |
| D15 | Persistence | No health result, last-value, or JetStream storage | Periodic snapshots and expiry meet the stated requirement |

## D01: consistency guarantee

Recommendation: promise bounded eventual convergence, not simultaneous
consistency.

Rationale:

- messages are live and at-most-once;
- partitions are possible;
- receiver freshness uses local time;
- no broker or local result persistence exists; and
- there is no acknowledgement from every instance.

Required wording is defined in
[Contract and convergence](03-contract-and-convergence.md).

Alternative: strong consistency requires an authority or consensus group,
durable state, quorum behavior, and an availability tradeoff. That is a
different feature.

## D02: health data category

Recommendation: model health as versioned, expiring snapshots outside the
common event envelope and storage publisher.

Rationale: health is recalculated, superseded, high-volume current state. The
existing event system is append-only, fsyncs accepted facts, and plans durable
ordered consumption. Routing each attempt through it would add unwanted
persistence and couple this feature to the unimplemented site journal.

Open point: whether stable local transitions may appear in the diagnostic
application log. Recommendation: allow rate-limited transition logs, but never
persist the distributed result stream.

## D03: transport

Recommendation: use one fixed versioned Core NATS subject and JSON payload.

Rationale: the cluster already spans all site instances and JetStream is
disabled by design. A fixed subject avoids unsafe authored tokens. JSON matches
existing contracts and is adequate at expected health-check rates.

Alternative: one subject per service enables broker filtering but requires
strict name encoding and many subscriptions. It provides no benefit when every
instance needs the complete view.

## D04: monitor lifetime

Recommendation: start monitoring after the process NATS clients connect and
stop it before those clients close.

Rationale: both roles must probe, and ownership can change many times during one
process. Active-site composition is currently unavailable and is the wrong
lifetime.

## D05: target identity

Recommendation: key a deployed service unit by project, environment, site,
machine, and service. Use service role as metadata.

Rationale: a master and slave copy commonly share the same service name on
different machines. Machine names are unique in the project today. Including
scope protects against accidental cross-deployment traffic.

Open point: if one machine must later host multiple instances of the same
service name, the blueprint needs an explicit service instance identifier. Do
not invent one in this feature.

## D06: observer identity and ordering

Recommendation: retain independent Primary and Standby observations. Order each
with process-start epoch and a process-local sequence.

Rationale: selecting whichever message arrived last is nondeterministic across
receivers. Combining independent observer slots is deterministic and preserves
disagreement. The existing durable process-start count fences messages from an
older process without persisting health results.

Do not use Active or Passive state as authority. It changes during the process
and both observers are required.

## D07: publication cadence

Recommendation: publish the current stable observation after every completed
attempt, including unchanged Healthy and Unhealthy states.

Rationale: transition-only publication cannot repair message loss or initialize
a late subscriber without replay. Every-attempt snapshots bound recovery by the
probe interval.

Alternative: transition plus a separate heartbeat has the same traffic order
and a more complex state machine.

## D08: freshness

Recommendation: derive freshness as `2 * interval + timeout` and measure it from
receiver arrival.

Rationale: this tolerates one lost periodic snapshot and avoids cross-machine
clock dependence. `retries` does not belong in freshness because every failed
attempt also publishes.

Open point: accept the exact multiplier after load and network analysis. If
changed, keep it derived and identical in every descriptor.

## D09: aggregation

Recommendation:

- no fresh observers: Unknown;
- all fresh observers Healthy: Healthy;
- all fresh observers Unhealthy: Unhealthy;
- fresh disagreement: Degraded.

Rationale: one stopped platform instance should not erase a live observation
from its redundant peer. Disagreement should remain visible instead of choosing
the Active observer or masking failure.

Alternative: "any unhealthy means Unhealthy" is safer for automated blocking
but conflates target failure with observer disagreement. Revisit before health
drives service activation. Automated activation is out of scope now.

## D10: HTTP probe semantics

Recommendation:

- machine descriptor IP as host;
- HTTP GET;
- 200 through 299 as success;
- redirects disabled;
- proxy disabled;
- body ignored and bounded;
- one success recovers;
- configured consecutive failures mark Unhealthy.

Rationale: the existing blueprint provides only type, port, path, and timings.
These rules make that contract complete without adding speculative options.
Machine IP supports multiple logical machines on one Windows scenario host.

Open points:

- whether query strings in `path` are supported;
- whether shared health ports are valid; and
- whether a future HTTPS type uses Windows certificate stores or authored file
  paths.

## D11: public query

Recommendation: add `GET /health/services`, served by both Active and Passive
instances, returning HTTP 200 for all valid status views.

Rationale: service state is query data. It is not a failure of the platform API.
Both processes build the same view and operators need to inspect either one.

Alternative: add service entries to the existing `checks` map. That shape loses
identity, observer disagreement, freshness, and typed SDK models.

## D12: local redundancy coupling

Recommendation: target service health never changes platform readiness,
platform Unhealthy status, lease renewal, promotion, or failback.

Rationale: both platform instances probe the same targets on one machine.
Transferring the platform listener does not repair a target and can create
ownership churn.

Future service activation may consume the site health view through a narrow
query contract. It must make its own election and fencing decisions.

## D13: NATS route security

Recommendation: require mutually authenticated and encrypted routes for
production. Provision private material outside the compiled descriptor. Keep
the client listener in-process or loopback-only.

Rationale: false health data can mislead operators and future automation. The
current cluster name check is routing configuration, not authentication.

If certificate infrastructure is not ready, explicitly classify the first
release as trusted-network-only and prohibit automated service actions based on
the view.

## D14: distribution abstraction

Recommendation: define small publish, subscribe, and clock interfaces near the
health consumer. Implement NATS and deterministic test fakes. Do not implement
SQLite or a site-level `distribution_type`.

Rationale: the current request explicitly selects NATS. The older distribution
draft is not reflected in current blueprint or runtime architecture. A broader
adapter can extract the proven narrow contract later.

## D15: persistence

Recommendation: add no health file, retained NATS message, JetStream stream, or
durable consumer.

Rationale: all state is periodically recalculated. Static inventory provides
known Unknown entries, and fresh snapshots repopulate observers.

Operational consequence: a restarted instance reports remote services Unknown
until new reports arrive. This is expected behavior, not data loss to repair.

