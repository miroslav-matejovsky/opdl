# Contract and convergence

## Observation model

The health message is a versioned state snapshot, not an event envelope. A
recommended logical shape is:

| Field | Purpose |
| --- | --- |
| `schema_version` | Reject or route incompatible payloads |
| `project`, `environment`, `site` | Prevent cross-deployment contamination |
| `machine`, `machine_profile` | Identify the target machine |
| `service`, `service_role` | Identify and describe the target service |
| `observer_role` | Fixed platform role: primary or standby |
| `observer_process_epoch` | Fence an older process incarnation |
| `sequence` | Order snapshots from one observer process |
| `status` | Unknown, Healthy, or Unhealthy |
| `checked_at_utc` | Diagnostic wall-clock time, not ordering authority |
| `latency_ms` | Last attempt duration |
| `consecutive_failures` | Retry state |
| `attempt_outcome` | Success, HTTP status failure, timeout, connection, protocol, or internal |
| `http_status` | Optional response status |
| `fresh_for_ms` | Receiver-relative observation lifetime |

Do not distribute raw response bodies. Avoid raw error strings in the stable
contract. A bounded diagnostic detail can be logged locally, while the message
uses a controlled failure category.

All fields must be size-bounded and validated before application. A malformed,
wrong-site, unknown-service, unknown-observer, impossible-duration, or
unsupported-version message is dropped and counted.

## Reporter identity and ordering

The view stores observations by:

```text
target machine + target service + observer role
```

`observer_process_epoch` comes from the existing per-instance state file's
process-start count. It increases before the process starts monitoring.
`sequence` is a process-local monotonically increasing counter.

Comparison rules:

1. A higher process epoch replaces a lower process epoch.
2. Within one process epoch, a higher sequence replaces a lower sequence.
3. Equal versions are duplicates and change nothing.
4. A lower version is ignored.

`checked_at_utc` must not order messages. Machine clocks can differ. It is for
operators only.

The implementation must preserve the process-start count in `app.process`.
Using the general instance epoch is weaker because activation advances that
value while the monitor continues running. Observer ordering should not reset
when Primary Ownership moves.

## Freshness

Every completed probe republishes a snapshot. The recommended freshness window
is derived, not separately authored:

```text
fresh_for = 2 * interval + timeout
```

This tolerates one missed periodic publication and one full request timeout. It
does not multiply by `retries`, because a snapshot is sent after every attempt,
including attempts below the unhealthy threshold.

The receiver records:

```text
expires_at = local_receive_time + fresh_for
```

It uses a monotonic local clock for expiration. The sender's wall clock does not
control freshness. The receiver validates `fresh_for` against the static
inventory or a safe derived bound so a malformed sender cannot remain fresh
forever.

Expiration removes the observation from reduction but may retain its last value
as explicitly stale diagnostic data. Expiration means the observer is silent.
It does not by itself prove that the target service is Unhealthy.

## Deterministic service reduction

For one service unit, consider only fresh observations:

| Fresh observations | Derived service status |
| --- | --- |
| None | Unknown |
| One or more, all Healthy | Healthy |
| One or more, all Unhealthy | Unhealthy |
| Healthy and Unhealthy disagree | Degraded |

Expected observers that are absent or stale are reported separately. They do
not force a service with one fresh observer to Unknown. This preserves useful
service health when one platform instance is stopped.

The view should expose:

- expected observer count;
- fresh observer count;
- missing or stale observer roles;
- each observer's stable status and retry count; and
- the derived service status.

This reducer is commutative and independent of message arrival order after the
newest snapshot for each observer is selected.

## Delivery semantics

The current Core NATS deployment provides:

- live fan-out to connected subscribers;
- per-connection publication ordering;
- no retained last value;
- no replay for late subscribers;
- no acknowledgement from each site instance; and
- no delivery across a route outage.

Health distribution therefore promises at-most-once delivery per publication.
It does not promise that every instance receives every observation.

Correctness comes from snapshots:

- every attempt republishes the complete current observer state;
- loss of one message is repaired by a later message;
- duplicates are harmless;
- out-of-order older messages are ignored;
- silent observations expire; and
- restart requires no replay.

## Consistency guarantee

The requested design cannot provide a simultaneous strongly consistent view on
every instance. During a NATS partition, each side receives different facts.
Without persistence, consensus, acknowledgement, or a snapshot authority, no
instance can know that every peer has the same set.

The recommended documented guarantee is:

> For a stable site topology and connected NATS cluster, all running platform
> instances converge to the same service status after they receive the newest
> observation from each live observer. A missed observation is repaired by the
> next probe publication. During disconnection, startup warm-up, or freshness
> boundaries, views may differ and must expose their incompleteness.

Expected recovery time after routes reconnect is approximately:

```text
maximum configured probe interval + NATS propagation time
```

An implementation can reduce restart warm-up with an optional ephemeral
request/reply snapshot exchange. It is not recommended for the first slice.
Periodic full snapshots already satisfy the no-persistence requirement with
less protocol and no authority selection.

## Partition behavior

During a site partition:

- local checks continue;
- each instance updates its own local observations;
- each connected partition converges internally;
- remote observations eventually become stale;
- the platform ownership lease remains independent; and
- `/health/services` reports missing observers and distribution degradation.

When routes recover, the next periodic report for every local target repairs
the remote views. No old transitions need replay.

## Startup behavior

The view starts from static inventory:

- all services are present;
- all observer observations are missing;
- every derived service status is Unknown; and
- a warm-up summary states how many expected observers have reported.

The service-health API is available in this state. It must not return an empty
list that could be mistaken for "the site has no services."

Platform readiness does not wait for all service observations. A failed or
stopped service is exactly what the monitor exists to report. Blocking platform
readiness on target health would hide the result and could destabilize local
redundancy.

