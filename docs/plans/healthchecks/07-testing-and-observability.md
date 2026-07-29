# Testing and observability

## Current coverage

| Area | Status |
| --- | --- |
| Blueprint, resolver, descriptor, and conformance | Implemented |
| HTTP prober, retry state, cancellation, monitor lifecycle, and concurrency | Implemented |
| Wire validation, bounds, publisher coalescing, and subscriber cleanup | Implemented |
| Reducer ordering, Unknown handling, freshness, inventory, and concurrency | Implemented |
| App startup, startup failure, ownership transitions, and reverse shutdown | Implemented |
| Real NATS peer delivery and sender no-echo on one embedded broker | Implemented |
| Public API, OpenAPI, and generated SDK | Implemented |
| Windows black-box health convergence, target transitions, observer loss, restart, and failover | Implemented |
| .NET E2E nonzero test discovery gate | Implemented |
| Startup jitter and supported-load validation | Implemented |
| Routed multi-broker partition and recovery | Pending; no partition mechanism exists on a developer host |

## Test layers

### Blueprint and descriptor

Required cases:

- all health fields resolve without loss;
- multiple services preserve authored order;
- site inventory is identical in every site machine descriptor;
- expected observers are primary-only or primary-and-standby correctly;
- local service and site inventory identity match;
- malformed paths, durations, retry counts, roles, and types fail;
- absent structured JSON fields fail platform loading;
- builder and platform descriptor signatures remain conformant; and
- package output embeds the new descriptor shape.

### Probe engine

Use table-driven tests and `require` assertions.

Required cases:

- 2xx is success;
- 1xx, 3xx, 4xx, and 5xx are failure;
- redirects are not followed;
- timeout is distinct from connection refusal and HTTP failure;
- response bodies are not interpreted;
- environment proxy is not used;
- first result starts from Unknown;
- `retries - 1` failures do not mark a previously Healthy target Unhealthy;
- the threshold failure marks Unhealthy;
- one success restores Healthy and clears failure count;
- scheduling never overlaps one target;
- one slow target does not block another;
- process cancellation cancels in-flight requests; and
- startup jitter stays within its bound and differs by observer identity.

Time-dependent tests should inject a clock or scheduler. Do not use sleeps.

### Contract and reducer

Required cases:

- higher process epoch wins;
- higher sequence in one epoch wins;
- duplicate and older snapshots are idempotent;
- sender timestamps do not affect ordering;
- wrong site, unknown service, unknown role, bad status, bad duration, oversized
  payload, and unsupported version are rejected;
- no fresh observer produces Unknown;
- all fresh Healthy produces Healthy;
- all fresh Unhealthy produces Unhealthy;
- disagreement produces Degraded;
- one fresh observer remains usable while its redundant observer is missing;
- expiry uses receive time;
- expiration and a later fresh message recover;
- every permutation of the same newest observations produces the same result;
  and
- returned snapshots cannot mutate internal state.

Fuzz the message decoder and reducer boundary. The invariant is that invalid
input never panics, allocates without a bound, or changes a valid existing view.

### NATS adapter

Required cases:

- subscription is active before first publication;
- all subscribers receive a live report;
- the publisher connection does not receive its own message;
- a second connection receives the same live report;
- a disconnected route loses messages as documented;
- the next full snapshot repairs the view after reconnect;
- bounded pending storage retains the newest snapshot per key;
- queue saturation and publish errors are reported;
- cancellation unsubscribes and closes cleanly; and
- no JetStream storage or result file is created.

Do not use the durable `eventfabric.Consumer` stub for these tests. Its
at-least-once replay behavior is intentionally different.

### Runtime and API

Required cases:

- Primary-only process starts a monitor;
- Primary and Standby both start monitors;
- subscriber setup and flush complete before the first probe;
- monitor shutdown precedes publisher, subscriber, and connection shutdown;
- Passive endpoint serves `/health/services`;
- Active and Passive status changes do not restart workers;
- response service and observer ordering is stable;
- unhealthy targets return HTTP 200 with status data;
- target failure does not make platform `/health` Unhealthy;
- target failure does not satisfy peer failover health;
- monitor subsystem failure is distinct from target failure;
- API shutdown precedes health view teardown; and
- OpenAPI and .NET SDK match the source types.

`sdk-dotnet/tests/Opdl.Sdk.E2E/test.runsettings` sets `TreatNoTestsAsError`, so
a run that discovers nothing fails. The `scenarios/sdk` category runs those
tests against a live instance and asserts each named test reported `Passed`
rather than trusting the exit code, because a skipped test is a passing run.

### Windows black-box scenarios

The `scenarios/health` category deploys two machines on separate loopback
addresses — node-a with a Standby Instance, node-b without — each with a
controllable service bound at the probe address its own blueprint authored.
Every assertion runs against all three instances at once, because convergence is
the claim being tested.

| Scenario | Covers |
| --- | --- |
| One machine, one observer | node-b, which deploys no Standby |
| One machine, two observers | node-a, whose service both instances report on |
| Two machines, every instance converges | The whole site, compared across the three views rather than trusting one |
| A target answers 503 and then 200 again | Transition and immediate recovery, with the probe's error and failure count carried to observers that never probed it |
| One platform instance is killed and restarted | Expiry by name, and reconstruction of the whole site — including the machine it is not on — from traffic that arrived after the restart |
| Ownership fails over while checks continue | The instance that takes over was already probing, so the machine's services are never unwatched |
| No health-result file is written | Every file each machine's blueprint authored is named, and anything else under the work directory fails |

A route outage and its recovery remain uncovered. Cutting a NATS route while
both brokers stay up needs privileged network control, and killing a process is
a machine outage rather than a partition. What is covered instead is the
observable half of the same behavior: an observer that stops reporting expires,
is named on every surviving instance, and does not change a target's status
while another observer is current.

All waits are bounded polls. The category contains no sleep.

## Observability model

Three failures must remain distinct:

| Condition | Meaning |
| --- | --- |
| Target failure | The HTTP service did not meet its health contract |
| Monitor failure | The platform could not schedule, execute, or reduce checks |
| Distribution failure | Local observations could not reach or be refreshed at remote instances |

Each has its own field, and none of them can be read from another's:

| Where | Field |
| --- | --- |
| Target failure | `services[].status` on `/health/services` |
| Monitor failure | `checks.serviceMonitor` on `/health`, which reports this instance's own observations about its own machine having expired |
| Distribution failure | `distribution.state` and its counters on `/health/services` |

`checks.eventFabric` on `/health` is a fourth thing and is not distribution: it
round-trips a message through this instance's own embedded broker, so it passes
while every route to every peer is down.

[Service health](../../04-service-health.md) is the operator's reading of these,
including which of them to look at for a given symptom.

## Logs

Recommended logs:

- monitor started and stopped, with target count;
- stable local target transition, at Info or Warn as appropriate;
- publication path disconnected and recovered;
- bounded queue overflow, with count and no payload;
- invalid inbound message, rate-limited by reason;
- observer became stale or fresh, logged only on transition; and
- shutdown timeout or worker leak.

Do not log every successful attempt. Do not log response bodies. Avoid logging
full NATS payloads or repeated connection failures without rate limiting.

The no-persistence requirement applies to health results and the health view.
Existing application logs remain diagnostic files, not a replayable health
record. Health observations are not appended to the event journal.

## Counters

Implemented counters cover publication, supersession, rejection, and apply
outcomes, and `distribution` on `/health/services` exposes them. Every reason is
rendered including the ones at zero, so an operator can see that something is
not happening without first proving the reason exists. Keep bounded
process-local counters for:

- attempts by outcome;
- stable state transitions;
- publish successes and failures;
- pending snapshot replacements;
- invalid inbound messages by controlled reason;
- stale observer transitions; and
- current fresh, stale, missing, and disagreeing observer counts.

Avoid unbounded labels from service names in a future metrics exporter. The API
already provides per-service detail.

## API diagnostics

The service-health response should report:

- local distribution connection state;
- total expected services;
- status summary;
- total expected, fresh, missing, and stale observers;
- view warm-up completeness;
- last accepted remote observation time; and
- per-service observer details.

It must not claim cluster-wide completeness merely because the local NATS
round-trip passes. The implemented `distribution.state` therefore asks only
about expected observers on *other* machines: an instance's own observations
reach its view directly, so counting them would report a working site through a
broker that had stopped carrying anything. `Connected`, `Partial`, `Isolated`,
and `Local` are the four answers, and view completeness is inferred from fresh
expected observers exactly as this section requires.

Warm-up completeness and last-accepted-remote-observation time are not separate
fields. Both are readable from what is there: a warming view has entries in
`missingObservers`, and the most recent remote report is the smallest `ageMs`
among observations from another machine.

## Performance bounds

Accepted and measured. The envelope is published in
[Service health](../../04-service-health.md); this is where it comes from.

| Bound | Value | Where it is enforced or measured |
| --- | --- | --- |
| Machines per site | 8 | `healthfabric/load_test.go` |
| Services per machine | 16 | `servicehealth/load_test.go` |
| Minimum probe interval | 1s | Both |
| Maximum timeout | shorter than the interval, by descriptor validation | `blueprint.validateHealthCheck` |
| Maximum observation payload | 8 KiB | `healthfabric.MaxMessageBytes`, enforced on decode |
| Maximum pending publication keys | one per local service | The publisher's map is keyed by service; proven by conservation |
| API response size | 128 services, each with up to 2 observations | Snapshot render measured at the full inventory |
| Shutdown duration | one probe timeout | Asserted on a fully loaded monitor |

Expected steady-state attempt rate is:

```text
sum over services (deployed platform instances on machine / interval)
```

A Standby doubles local probe traffic by requirement, and every resulting
snapshot is fanned to every site instance. At the accepted envelope that is 256
observations per second, each delivered to 16 instances — about 180 KB/s of
site-wide health traffic at roughly 700 bytes per observation.

Both load tests run in the integration gate rather than on request, so the
envelope is re-measured on every full run rather than remembered from one.

Two things the measurements do not cover. The load test runs the whole site in
one process, which is harder on memory and delivery scheduling than eight
machines but easier on the network, so the network side of the envelope is
inferred. And connection recovery after a route outage is untested for the same
reason partition testing is: there is no way to cut a route between two live
brokers on a developer host.

## Implementation validation gate

At the end of each roadmap step:

1. run targeted tests while developing;
2. run race-enabled package tests where concurrency changes;
3. regenerate checked-in API artifacts when public types change; and
4. run `task all`.

No roadmap step is complete while `task all` fails.
