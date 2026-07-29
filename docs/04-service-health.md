# Service health

The platform watches the services a deployment declares, shares what it found
across the site, and answers what every instance currently knows. It does not
start, stop, or repair a service. Nothing it observes moves Primary Ownership.

This document is for whoever is reading `GET /health/services` at three in the
morning. The contract behind it is in
[the health-check plan](plans/healthchecks/README.md); the shape of the
descriptor it is built from is in [Architecture](01-architecture.md).

## What the platform actually does

Every platform instance probes every service on **its own machine**, for the
whole life of the process, in every ownership state. A machine that deploys a
Standby therefore has two observers on each of its services; a machine that does
not has one.

Each observation is published to the site over a dedicated Core NATS subject and
folded into every instance's in-memory view. Nothing is written to disk and
nothing is replayed. A restarted instance knows nothing until new reports
arrive, which is expected behavior rather than data loss.

Because there is no persistence and no acknowledgement, the guarantee is bounded
eventual convergence: instances agree once connectivity holds and one probe
interval has passed. They may briefly disagree, and the API is built so that
disagreement is visible rather than resolved behind your back.

## Reading a status

| Status | What it means | What to do |
| --- | --- | --- |
| `Healthy` | Every current observer found the service answering. | Nothing. |
| `Unhealthy` | Every current observer found it failing, past its retry count. | Look at the service. `observations[].error` is what the probe got. |
| `Degraded` | Current observers **disagree** about it. | Look at the network between the observers and the service, not at the service alone. |
| `Unknown` | Nothing current says anything about it. | Read the observer lists below before concluding anything. |

`Degraded` is never reported by an observer. It only ever comes out of reducing
several, so it always means the observers disagree — typically one machine can
reach the service and another cannot, or one instance's probe is timing out
where the other's is not. Treat it as a question about reachability.

`Unknown` has three distinct causes, and the response tells them apart:

- `missingObservers` is not empty — those instances have never reported here.
  Either they have just started, or they are not running.
- `staleObservers` is not empty — they reported and then stopped. That is an
  observer or a fabric problem, not a service problem.
- neither is set, and the observations all say `Unknown` — the observers are
  running and have not resolved the service yet. Its probes are failing below
  the retry threshold. `consecutiveFailures` shows how far down it is.

A fresh `Unknown` observation contributes no verdict. `Healthy` plus `Unknown`
reduces to `Healthy`, because an observer that has not decided is not
disagreeing.

## Reading the observers

Missing and stale are deliberately separate. An observer that never started and
one that went quiet are different faults with different fixes.

| Condition | Likely cause | First check |
| --- | --- | --- |
| An expected observer is **missing** shortly after a deployment or restart | It has not completed its first interval. Startup jitter delays a worker's first probe by up to one interval. | Wait one interval and re-read. |
| An expected observer is **missing** and stays missing | That platform instance is not running, or never connected to the site. | Its own `/health` and `/instance`, and whether its process exists. |
| An expected observer is **stale** | It stopped publishing: process gone, broker gone, or route down. | `distribution.state` on the instance you are asking, and the stale instance's own endpoint. |
| Only **remote** observers are stale, everywhere | The fabric, not the observers. | Routes between the machines' embedded brokers. |

A stale observation is kept and shown rather than deleted. What it last said is
still the most recent thing anyone here knows; that it has aged is a separate
fact, carried as `stale` and `ageMs`.

Freshness is `2 * interval + timeout`, measured from when the report **arrived
here**, never from the sender's clock. Machines at a site do not share a clock,
so `checkedAtUtc` and `receivedAtUtc` are both shown: a large gap between them
is either a slow site or a machine whose clock is wrong, and neither one alone
tells you which.

## Reading the distribution state

`distribution.state` answers one question: are observations from the rest of the
site arriving at the instance you asked?

| State | Meaning | What to do |
| --- | --- | --- |
| `Connected` | Every expected observer on another machine is current. | Nothing. |
| `Partial` | Some are current, some are missing or stale. | Identify which, from the `staleObservers` on each service. One machine is usually the cause. |
| `Isolated` | No expected remote observer is current. | This instance has lost the site. Its own machine's services are still being probed and are still accurate. |
| `Local` | The site expects no observer on another machine. | Nothing. A one-machine site has no remote traffic whose absence would mean anything. |

It asks only about observers on *other* machines. An instance's own observations
reach its view directly, so counting them would report a working site through a
broker that had stopped carrying anything.

This is **not** the same as the `eventFabric` check on `/health`. That one
round-trips a message through the instance's own embedded broker, so it passes
while every route to every peer is down. If you are diagnosing a partition,
`distribution.state` is the field that knows.

The counters beside it distinguish the remaining cases:

| Counter | Non-zero means |
| --- | --- |
| `publishFailed` | This instance could not hand observations to its broker. Local monitoring is fine; the site is not hearing it. |
| `superseded` | Observations were replaced before they were sent — the sender was backed up. Bounded by the machine's service count, never by outage length. |
| `delivered` at zero, with remote observers expected | Nothing is arriving. Routes. |
| `rejected[*]` | Messages arrived that were not well-formed reports about this deployment. See below. |
| `dropped[*]` | Well-formed reports the view refused. See below. |

Every reason is listed even at zero, so you can see that something is not
happening without first proving it exists.

## Rejected and dropped messages

| Reason | Meaning |
| --- | --- |
| `oversize` | A message past the wire bound. A sender running different code, or something else publishing on the subject. |
| `malformed` | Not decodable as an observation. |
| `version` | A different wire version. A site running mixed builds. |
| `foreign_deployment` | A report about a different project, environment, or site. Two deployments are sharing a broker; that is a misconfiguration, not a topology. |
| `incomplete` | Required fields missing. |
| `impossible` | Values that cannot be true — a zero sequence, a zero check timestamp, a latency past the bound. |
| `unknown_target` | A report about a service this site's inventory does not contain. A machine built from a different blueprint, or one left over from an older one. |
| `unknown_observer` | A report from a role not expected to observe that service, such as a standby report about a machine that deploys none. |
| `unusable_status` | A status an observer may not state, including the reduction-only `Degraded`. |
| `duplicate` | The same report twice. Harmless. |
| `stale` | A report older than what that observer's slot already holds. Expected during a restart, when the previous incarnation's messages are still in flight. |

The first message of each kind is logged; the rest are counted. A sender stuck
producing bad messages produces them at its probe interval, and a log line each
would bury everything else.

## What service health never does

- It never makes an instance report itself `Unhealthy` on `/health`. Both of a
  machine's instances probe the same services, so moving Primary Ownership
  repairs nothing and would hand a machine back and forth over a target neither
  instance controls.
- It never fails `/health/ready`, and never satisfies the peer promotion gate.
- It never appears in the durable event journal. Health is expiring current
  state, not a fact to replay.
- It never writes a file. If you find one, that is the bug.

The one platform check it does drive is `checks.serviceMonitor` on `/health`,
and that is about **this instance's own monitoring**, not about any service: it
reports `Unhealthy` when this instance's reports about its own machine have
expired, which means it has stopped watching. A stalled monitor degrades the
instance rather than failing it, for the same reason as above — the peer's
monitor is no better placed than this one.

So the three failures stay distinct:

| Where you see it | What failed |
| --- | --- |
| `services[].status` on `/health/services` | The service. |
| `checks.serviceMonitor` on `/health` | This instance's monitoring of its own machine. |
| `distribution.*` on `/health/services` | This instance's half of the site's health traffic. |

## Supported load

The platform is validated against this envelope. It is a commitment, not an
observed limit: exceeding it is untested rather than known to break.

| Bound | Value |
| --- | --- |
| Machines per site | 8 |
| Services per machine | 16 |
| Platform instances per machine | 2 |
| Minimum probe interval | 1s |
| Maximum probe timeout | shorter than the interval; 900ms at the 1s floor |
| Site inventory held by every instance | 128 services |
| Maximum observation on the wire | 8 KiB |
| Pending publications per instance | one per local service — 16, never more |

At that size the site produces 256 observations per second, each fanned to all
16 instances. Measured:

- one observation at the largest identity the envelope produces is about 700
  bytes, well inside the 8 KiB bound — roughly 180 KB/s of site-wide health
  traffic;
- the whole site in one process converges with no publication failures, no
  supersessions, and no rejections;
- a 128-unit snapshot — what the API renders on every request — costs
  microseconds; and
- a machine's 16 workers at the 1s floor stop within milliseconds, far inside
  the one-timeout shutdown budget a deployment allows.

The measurements live with the code: `internal/site/healthfabric/load_test.go`
and `internal/machine/servicehealth/load_test.go`. They run in the integration
gate, so the envelope is re-measured rather than remembered.

Two things are deliberately **not** covered. A true route partition — both
brokers up, the route between them down — is not reproducible on a single
developer host without privileged network control, so partition behavior is
inferred from Core NATS semantics and from observer expiry, which is a related
but different fault. And the envelope has not been measured on eight physical
machines; the load test runs the whole site in one process, which is harder on
memory and delivery scheduling but easier on the network.

## Upgrading

The descriptor's health contract is **not backward compatible**. A deployment
package built before it cannot be mixed with one built after it, and the
platform will not load an older descriptor.

What changed:

- `services` was a list of names. It is now a list of structured service
  definitions, each carrying `role` and a full `health_check` block: `type`,
  `port`, `path`, `interval`, `timeout`, `retries`.
- `site_services` is new. It is the whole site's service inventory, carrying
  identity, expected observer roles, and derived freshness, and deliberately no
  endpoint.
- `lease.lag_bound` was removed from the blueprint, the descriptor, and the
  platform configuration. A blueprint that still declares it fails validation.

What this means for a rollout:

1. Rebuild every machine of a site from the same blueprint. A site running mixed
   builds will reject the other side's messages as `version` or
   `foreign_deployment`, and the mismatch shows up in `distribution.rejected`.
2. Remove `lease.lag_bound` from blueprints before rebuilding.
3. Author a `health_check` on every service. There is no default; a service
   without one fails blueprint validation.
4. Expect every remote service to read `Unknown` for up to one interval after a
   machine restarts. Nothing is restored from disk, by design.

`GET /health/services` is a new endpoint and adds no obligation on an existing
client. The `checks` map on `/health` did change: `internalServices`, which was
hardcoded to `Healthy` and meant nothing, is gone and replaced by
`serviceMonitor`, which reports something real. A client asserting on the old
key must be updated.
