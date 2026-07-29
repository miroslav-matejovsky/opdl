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
| Public API, OpenAPI, and generated SDK | Pending |
| Routed multi-broker partition and recovery | Pending |
| Windows black-box health convergence | Pending |
| .NET E2E nonzero test discovery gate | Pending |
| Startup jitter and supported-load validation | Pending |

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

The current `task sdk-dotnet` output reports that no E2E tests are available,
but still exits successfully. Fix discovery and make zero discovered tests fail
before treating this layer as service-health coverage.

### Windows black-box scenarios

These scenarios are pending the public query endpoint. Existing scenarios prove
process startup and lifecycle, but cannot yet inspect the site view.

Required scenarios:

1. One machine, Primary only, one service.
2. One machine, Primary and Standby, multiple services with different policies.
3. Two machines, all platform instances converge.
4. Target refuses connections, starts, returns 500, returns 200, then stops.
5. One platform instance restarts and reconstructs the site view.
6. A route outage makes remote observers stale while local probes continue.
7. Route recovery converges on the next report.
8. Platform ownership fails over and fails back without monitor interruption.

Use the existing `waitfor` polling utilities and bounded deadlines. Fake service
control should be explicit and deterministic.

## Observability model

Three failures must remain distinct:

| Condition | Meaning |
| --- | --- |
| Target failure | The HTTP service did not meet its health contract |
| Monitor failure | The platform could not schedule, execute, or reduce checks |
| Distribution failure | Local observations could not reach or be refreshed at remote instances |

The service API exposes target state and view completeness. Platform `/health`
exposes monitor and local broker subsystem state. Application logs explain
actionable failures.

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
outcomes. The public API still needs to expose the useful subset. Keep bounded
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
round-trip passes. "Connected" means the local adapter is connected to its
embedded broker. View completeness is inferred from fresh expected observers.

## Performance bounds

Before production rollout, define:

- maximum machines per site;
- maximum services per machine;
- minimum probe interval;
- maximum timeout;
- maximum observation payload size;
- maximum pending publication keys;
- maximum API response size; and
- maximum shutdown duration.

Expected steady-state attempt rate is:

```text
sum over services (deployed platform instances on machine / interval)
```

A Standby doubles local probe traffic by requirement. Every resulting snapshot
is fanned to every site instance. Load tests must cover the largest accepted
site, not only one service.

## Implementation validation gate

At the end of each roadmap step:

1. run targeted tests while developing;
2. run race-enabled package tests where concurrency changes;
3. regenerate checked-in API artifacts when public types change; and
4. run `task all`.

No roadmap step is complete while `task all` fails.
