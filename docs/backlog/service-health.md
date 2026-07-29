# Distributed service health

Status: Steps 00 and 01, and the local probe engine, completed on 2026-07-29.
The remaining work is backlogged below.

The deployment descriptor contains local service probe policy and a site-wide
service inventory. Builder, platform, and conformance validation enforce the
same contract.

Both platform instances probe every service on their machine for the whole
process lifetime, in `platform/internal/machine/servicehealth`. Every attempt is
applied to the process's own site view and published on
`opdl.service_health.v1`, and every instance keeps a converged in-memory picture
of the whole site in `platform/internal/site/healthview`.

What is missing is the way out: nothing reads the view yet. `GET
/health/services` is the next item, and until it exists the site's health is
maintained correctly and cannot be asked about.

## Accepted design

Keep these decisions unless a later backlog item explicitly changes them:

- Treat health as an ephemeral current-state snapshot. Do not persist it and do
  not use JetStream.
- Run an independent worker for every local service in both Primary and Standby
  processes for the whole process lifetime. Ownership changes do not affect
  monitoring.
- Probe with HTTP GET against the descriptor machine IP. Treat 2xx as success.
  Disable redirects and proxy use.
- Publish the current stable observation after every attempt on the fixed Core
  NATS subject `opdl.service_health.v1` using a bounded JSON contract.
- Identify a target by project, environment, site, machine, and service.
  Identify an observer by target, fixed platform role, process-start epoch, and
  process-local sequence.
- Apply only a newer process epoch, or a newer sequence within the same epoch.
  Ignore duplicates and older observations.
- Calculate report freshness at the receiver from arrival time. Use the
  descriptor's exact `fresh_for = 2 * interval + timeout`.
- Reduce fresh observations to Healthy, Unhealthy, Degraded, or Unknown. No
  fresh observations means Unknown. Agreement produces Healthy or Unhealthy.
  Disagreement produces Degraded. Report missing and stale expected observers
  separately.
- Expose `GET /health/services` from every platform instance. A valid view
  returns HTTP 200 regardless of contained service states.
- Service health must not affect platform readiness, ownership, lease renewal,
  promotion, or failback.
- Keep health package interfaces narrow. Do not introduce a general messaging
  abstraction for this feature.

## Done: local probe engine

Implemented in `platform/internal/machine/servicehealth`, composed by
`platform/internal/app`.

`servicehealth.Monitor` runs one worker per target. A worker probes, folds the
outcome into that target's stable status, hands an `Observation` to the sink,
and waits one interval before probing again — waiting after the attempt rather
than on a fixed schedule, so attempts against one target never overlap. The
retry policy lives in an unexported `tracker` with no clock and no transport, so
the state table is tested directly.

The clock, prober, and sink are injected. `app.startServiceHealth` supplies the
real three and is where monitoring's lifetime is decided: it starts after the
broker and before ownership management, and stops before the fabric closes, so
both roles probe across every ownership change.

Two things were settled while implementing it. The probe URL is composed by
appending the authored path to the machine ip and port rather than through
`url.URL`, because `url.URL` re-encodes what it renders and the authored bytes
are the contract; the composed URL is parsed back to confirm the host is this
machine. And `SinkFunc` was dropped as a speculative adapter with no caller.

Carried into the next item: the sink is `app.healthLog`, which writes stable
transitions to the application log and drops repeats. It is a placeholder for
the distribution sink, not a second consumer to keep.

Required behavior, all implemented:

- Start with Unknown.
- One successful attempt changes the stable status to Healthy and resets the
  failure count.
- A failed attempt increments the consecutive failure count. Change the stable
  status to Unhealthy when the count reaches `retries`.
- A sub-threshold failure preserves the prior stable status and exposes the
  pending failure count.
- Publish a snapshot after every completed attempt, including unchanged states.
- Treat non-2xx responses, timeouts, connection errors, and protocol errors as
  failed attempts. Process shutdown cancellation is not a target failure.
- Use injected clock, prober, and observation sink seams. Keep tests
  deterministic and free of sleeps.

Acceptance, met: the state table is covered directly, and the worker is covered
for initial failure, threshold crossing, recovery, timeout, shutdown mid-probe,
independent targets, and worker joining. A leaked worker hangs `Stop` rather
than failing an assertion, which is the strongest form that check can take. The
tests use a controlled clock and a channel sink, so none of them sleeps.

## Done: NATS distribution and site reducer

Implemented as two packages, split the way `events` and `eventfabric` are:
`platform/internal/site/healthview` is the picture, and
`platform/internal/site/healthfabric` is what carries reports into it.

`healthview` holds no transport and no wire format. It is built from the static
inventory, keeps a slot per expected observer rather than collapsing to
last-writer-wins, fences on `(epoch, sequence)`, expires by arrival on its own
clock, and reduces at snapshot time. Nothing expires in the background: a report
nobody looked at while it aged out did not need sweeping.

`healthfabric` opens a second connection to the same broker, beside the event
fabric's — durable facts and expiring current state are not the same traffic.
Its publisher never blocks its caller: it holds the latest observation per
service rather than a queue, so the buffer is bounded by the machine's service
count instead of by the length of an outage, and supersessions are counted.

Three things settled while implementing it:

- **An observer reporting Unknown contributes no verdict.** D09 does not cover
  it, and it is a real state — an instance whose first probes have failed below
  its retry threshold reports Unknown. Counting it as agreement or disagreement
  would let a starting instance drag a service's answer around. It is fresh and
  present, and silent on the question.
- **`Publish` returns the stamped observation**, which the caller applies to its
  own view. That way there is one place a sequence is assigned, and this
  instance's own slot is ordered by the same numbers its peers receive.
- **The health connection flushes with its own deadline.** The NATS client
  refuses a context without one and the caller's is the process context. The
  scenarios caught this: without it, every instance failed at startup with
  `nats: context requires a deadline`.

Required behavior, all implemented:

- Subscribe and flush the subscription barrier before starting local workers.
- Apply a local observation to the local view before enqueueing publication.
- Validate payload version, size, deployment identity, inventory membership,
  observer role, status, sequence, epoch, timestamps, and latency before
  applying it.
- Drop and count malformed, wrong-site, unknown-target, unknown-observer,
  impossible-duration, duplicate, and stale messages.
- Never block probe scheduling on a slow or disconnected publisher. Keep only
  bounded current-state work and report overflow.
- Initialize every inventory unit as Unknown. Rebuild the view from periodic
  snapshots after restart or route recovery.
- Expire observations using receiver-relative monotonic time. Keep sender wall
  time only as diagnostic data.
- Preserve the process-start count as the observer epoch. Do not use an
  ownership epoch.

Acceptance, met by unit tests that use a controlled clock and a fake connection:
ordering and fencing, duplicate suppression, restart reconstruction (a new
epoch's first report supersedes the previous incarnation's last), expiry by
arrival, the disagreement table, bounded backpressure under a stalled
connection, and every reject and drop counted by reason.

Not yet covered, and deliberately: route partition and recovery across real
machines. That is a black-box property and it needs an endpoint to observe, so
it belongs to the convergence scenarios below.

## P0: process lifecycle and public API

Effort: Large

Value: High

What remains of this item is the public API. Composition is done: `app.Run`
validates the descriptor, opens the broker, advances the process-start epoch,
builds the view, subscribes, opens the publisher, starts the monitor, and stops
all of it in reverse. `app.startServiceHealth` unwinds what it opened on any
failure, and a process that cannot watch its machine's services states
`platform.app.service_health_start_failed` and stops.

The view is built and converged and has no reader. That is the gap: adding the
endpoint is a matter of rendering `healthview.Snapshot`, which is already
deterministic and already carries everything the response shape needs.

Required behavior:

- Validate the descriptor before opening runtime resources. (done)
- Advance the process-start epoch before monitoring begins. (done)
- Start the embedded NATS server, distribution connection, subscription, view,
  workers, and HTTP listener in dependency order. (done)
- On shutdown, stop workers, drain publication, unsubscribe, close the health
  connection, and then close dependent resources without losing errors. (done,
  except that pending publications are dropped rather than drained: an
  observation from a process that is stopping is about to be superseded by
  nothing at all, and the site should see its silence.)
- Add `GET /health/services` to public Go API types, OpenAPI generation, the
  generated .NET client, and HTTP routing. Re-add `serviceHealth.View`, removed
  because nothing read it and `task deadcode` fails an unreachable function.
- Return deterministic ordering and include target identity, aggregate status,
  observer status, freshness, missing observers, last check time, latency, and
  pending failures.
- Extend platform `/health` with monitor and local distribution subsystem state.
  Keep target service status separate.

Dependencies: local probe engine and site reducer.

Acceptance: lifecycle rollback and shutdown tests pass for both roles. OpenAPI
and generated clients match source types. Add executable .NET client tests so
the end-to-end gate does not report zero discovered tests.

## P1: black-box convergence and failure scenarios

Effort: Large

Value: High

Extend the Windows simulation with controllable HTTP target services and API
assertions.

Scenarios must cover:

- Primary-only and Primary-plus-Standby machines.
- Two machines where all platform instances converge.
- Connection refusal, service start, HTTP 500, HTTP 200, and service stop.
- Retry threshold and one-success recovery.
- One platform process restart and view reconstruction.
- Route outage with continuing local probes, remote expiry, and recovery.
- Platform ownership changes without probe interruption or health-driven
  promotion.

Dependencies: public API composition.

Acceptance: scenarios assert eventual state within policy-derived bounds and do
not depend on fixed sleeps.

## P0 before production: route security and operational hardening

Effort: Large

Value: Critical

D13 remains open. Select and implement mutually authenticated, encrypted NATS
routes before production rollout. Cluster name alone does not protect health
data integrity.

Recommendation: use mutual TLS with machine identities issued by the deployment
environment. Define certificate provisioning, Windows certificate store or file
loading, rotation, expiry behavior, and trust-root rollout before coding.

If certificate infrastructure is not ready, classify the release as
trusted-network-only and prohibit automated actions based on the health view.

Also add:

- bounded payload, queue, target-count, and diagnostic-detail limits;
- logs and metrics for monitor lifecycle, stable transitions, publish
  disconnect/recovery, queue overflow, rejected messages, stale observers, and
  view warm-up;
- load tests at maximum supported machine and service counts;
- operator documentation for Unknown, Unhealthy, Degraded, stale observers, and
  route outages.

Dependencies: distribution and API behavior must be stable.

Acceptance: threat model and certificate lifecycle are documented, security
tests pass, limits are enforced, and production rollout criteria are explicit.

## Deferred decisions

These do not block the current HTTP-only implementation:

1. Freshness multiplier. Keep `2 * interval + timeout`. Revisit only with
   measured false-stale or slow-detection data.
2. HTTPS certificate sourcing. Prefer Windows certificate stores when HTTPS
   probes are introduced, unless deployment constraints require authored file
   paths.
3. Service instance identity. If one machine must host two copies with the same
   service name, add an explicit blueprint identifier. Do not infer one from
   endpoint details.
