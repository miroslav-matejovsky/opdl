# Distributed service health

Status: Steps 00 and 01 completed on 2026-07-29. The remaining work is
backlogged below.

The deployment descriptor now contains local service probe policy and a
site-wide service inventory. Builder, platform, and conformance validation
enforce the same contract. Health observations, NATS distribution, reduction,
and the public API are not implemented yet.

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

## P0: local probe engine

Effort: Medium

Value: High

Implement typed local targets, an HTTP prober, and one cancellation-aware worker
per target.

Required behavior:

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

Dependencies: completed descriptor contract.

Acceptance: state-table tests cover initial failure, threshold crossing,
recovery, timeout, shutdown, independent targets, and no worker leaks.

## P0: NATS distribution and site reducer

Effort: Large

Value: High

Add the versioned observation DTO, Core NATS publisher/subscriber, bounded
publication queue, and concurrency-safe in-memory site view.

Required behavior:

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

Dependencies: local probe engine.

Acceptance: deterministic unit and integration tests prove ordering, fencing,
expiry, disagreement reduction, duplicate suppression, restart reconstruction,
bounded backpressure, and route recovery.

## P0: process lifecycle and public API

Effort: Large

Value: High

Compose monitoring, distribution, reduction, and the HTTP endpoint into both
platform roles.

Required behavior:

- Validate the descriptor before opening runtime resources.
- Advance the process-start epoch before monitoring begins.
- Start the embedded NATS server, distribution connection, subscription, view,
  workers, and HTTP listener in dependency order.
- On shutdown, stop workers, drain publication, unsubscribe, close the health
  connection, and then close dependent resources without losing errors.
- Add `GET /health/services` to public Go API types, OpenAPI generation, the
  generated .NET client, and HTTP routing.
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
