# Contract and convergence

Status: implemented in `healthview` and `healthfabric`. The public query surface
and black-box convergence assertions are still pending.

## Observation model

The health message is a versioned state snapshot, not an event envelope. A
message on `opdl.service_health.v1` has this implemented JSON shape:

| Field | Purpose |
| --- | --- |
| `v` | Reject incompatible payloads |
| `project`, `environment`, `site` | Prevent cross-deployment contamination |
| `machine`, `service` | Identify the target service unit |
| `observer_role` | Fixed platform role: primary or standby |
| `epoch` | Fence an older platform process |
| `sequence` | Order snapshots from one observer process |
| `status` | Unknown, Healthy, or Unhealthy |
| `checked_at_utc` | Diagnostic wall-clock time, not ordering authority |
| `latency_ms` | Last attempt duration |
| `consecutive_failures` | Retry state |
| `error` | Optional bounded diagnostic detail |

Machine profile, service role, expected observers, and freshness are deliberately
absent from the wire message. Every receiver gets them from its validated static
inventory. Probe endpoints are also never distributed.

Messages are limited to 8 KiB. Diagnostic errors are truncated to 512 bytes.
Raw response bodies are never distributed. A malformed, wrong-site,
unknown-service, unknown-observer, zero-versioned, impossible-duration, or
unsupported-version message is rejected and counted before it reaches the
view.

## Reporter identity and ordering

The view stores observations by:

```text
target machine + target service + observer role
```

`epoch` is the durable instance epoch captured after it is advanced during
process startup. It remains fixed for the health publisher's lifetime.
`sequence` is a process-local monotonically increasing counter that starts at
one.

Comparison rules:

1. A higher process epoch replaces a lower process epoch.
2. Within one process epoch, a higher sequence replaces a lower sequence.
3. Equal versions are duplicates and change nothing.
4. A lower version is ignored.

`checked_at_utc` must not order messages. Machine clocks can differ. It is for
operators only.

Later ownership activation may advance the state file again, but it does not
change the publisher's captured epoch. A later process advances the durable
epoch before publishing, so its observations fence the previous process.
Primary Ownership movement does not reset observer ordering.

## Freshness

Every completed probe republishes a snapshot. The recommended freshness window
is derived, not separately authored:

```text
fresh_for = 2 * interval + timeout
```

This tolerates one missed periodic publication and one full request timeout. It
does not multiply by `retries`, because a snapshot is sent after every attempt,
including attempts below the unhealthy threshold.

The receiver records the arrival time on its own monotonic clock. At snapshot
time it computes:

```text
age = snapshot_time - local_receive_time
fresh = age <= fresh_for
```

It uses a monotonic local clock for expiration. The sender's wall clock does not
control freshness. The receiver validates `fresh_for` against the static
inventory or a safe derived bound so a malformed sender cannot remain fresh
forever.

The freshness boundary is inclusive. An observation becomes stale only when
its age is greater than `fresh_for`. No background expiry worker is needed.
Expiration removes the observation from reduction but retains its last value
as stale diagnostic data. It means the observer is silent, not that the target
service is Unhealthy.

## Deterministic service reduction

For one service unit, consider verdicts from fresh observations. A fresh
Unknown observation is present and visible in diagnostics but contributes no
verdict:

| Fresh verdicts | Derived service status |
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
newest snapshot for each observer is selected. A fresh Unknown cannot create or
remove disagreement. For example, Healthy plus Unknown is Healthy.

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
- the publisher returns the stamped observation for immediate local apply;
- the health NATS connection uses no-echo, so that local apply has one ordering
  path while peers still receive the publication;
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

