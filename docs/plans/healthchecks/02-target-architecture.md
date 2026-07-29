# Target architecture

## Ownership by level

The implementation follows the repository rule that packages are grouped by
the owner of their state.

| Package area | Responsibility |
| --- | --- |
| `internal/machine/servicehealth` | HTTP probing, retry state, local service observations, and probe scheduling |
| `internal/site/healthview` | Static inventory, per-observer slots, freshness, deterministic site reduction, and immutable query snapshots |
| `internal/site/healthfabric` | Versioned wire contract, bounded latest-value publication, NATS subscription, validation, and counters |
| `internal/app` | Descriptor adaptation, process identity, startup and shutdown ordering, and API composition |
| `api` and `internal/httpapi` | Public response types and `GET /health/services` registration |

The package names and boundaries are now implemented:

- machine logic does not import NATS, API, or deployment configuration;
- site logic does not start an embedded broker;
- neither package reads the embedded descriptor directly; and
- `internal/app` is the only place that knows the concrete broker, descriptor,
  process epoch, monitor, site view, and HTTP handler together.

The architecture allow-list and depguard rules must be updated with the new
components in the same step that introduces each package.

## Process lifecycle

Implemented startup order:

1. Load and validate the embedded descriptor.
2. Resolve the fixed platform instance role.
3. Open the application log, event log, machine event store, and state.
4. Advance the process epoch.
5. Start this instance's embedded NATS server.
6. Connect the existing event-fabric health client.
7. Construct the static site health view with every expected service Unknown.
8. Open the health distribution connection.
9. Subscribe to the health subject and flush the subscription barrier.
10. Open the publisher and start local probe workers with cancellation rooted
    in the process context.
11. Bind the platform HTTP listener and enter ownership management.

Implemented shutdown order:

1. Cancel probe scheduling so no new request starts.
2. Cancel in-flight probes through their request contexts.
3. Wait for probe workers to finish within a bounded shutdown context.
4. Stop accepting new distribution updates.
5. Abandon pending latest values and stop the publisher.
6. Unsubscribe and close the health distribution connection.
7. Close the existing event-fabric client.
8. Stop the embedded broker.

The API listener currently shuts down inside `runProcess` before `Run` closes
the broker. The health view can remain readable until that listener drains.

Invalid health configuration or failure to establish the local NATS
subscription is a platform startup error. A target service answering unhealthy
or refusing a connection is normal observed state and never a startup error.

## Local probe engine

Each service target has one independent worker in each running platform
instance. A simple worker-per-target model is preferred until measured service
counts justify a scheduler.

Worker behavior:

1. Execute an immediate first probe.
2. Wait one configured interval after each completed attempt.
3. Never overlap two probes for the same target.
4. Bound every request by the configured timeout and parent cancellation.
5. Publish one current observation after every completed attempt, even when
   stable health did not change.
6. Stop promptly when the process context is canceled.

Primary and Standby can currently issue their immediate probes together.
Deterministic observer-specific startup jitter remains Step 06 hardening work.

One successful attempt sets stable observer status to Healthy and resets the
failure counter. A failed attempt increments the counter. Stable status changes
to Unhealthy when consecutive failures reach `retries`. Before the first
success or threshold failure, it is Unknown. A failure below the threshold
retains the last stable status and exposes the pending failure count.

This gives `retries` a precise meaning and lets periodic publication repair
message loss without transition-only heartbeats.

## HTTP probe behavior

Recommended initial contract:

- method: GET;
- target host: the compiled descriptor machine IP;
- scheme: HTTP;
- healthy response: status 200 through 299;
- unhealthy response: every other status, timeout, DNS error, connection error,
  protocol error, or context cancellation not caused by platform shutdown;
- redirects: disabled;
- environment proxy: disabled;
- response body: not interpreted, drained only to a small fixed limit, then
  closed;
- client transport: reused per monitor, with idle connections closed at
  shutdown.

Using the machine IP supports the Windows scenario harness, where several
logical machines share one OS but bind different loopback IPs. It also avoids
silently assuming that a production service listens on 127.0.0.1. If loopback
or TLS targets are required, they should be explicit future probe options.

## Distribution path

The dedicated fixed Core NATS subject is:

```text
opdl.service_health.v1
```

Do not put authored machine or service names into subject tokens. Current
validation does not forbid NATS wildcard characters, dots, or whitespace inside
names. Identity belongs in the validated message payload.

Each instance:

- subscribes to the subject before local workers start;
- applies its local observation to its in-memory view;
- enqueues the newest observation for NATS publication;
- validates and applies received remote observations;
- disables NATS echo because the stamped local observation is applied directly;
  and
- evaluates freshness whenever a snapshot is read.

The publication path must not block probe scheduling indefinitely. Use a
bounded, per-observer latest-value queue. Replacing an older pending snapshot
with a newer one is safe because snapshots are not deltas. Queue saturation and
publish failure must be observable.

The health NATS adapter holds its own in-process `nats.Conn` behind narrow
interfaces declared near the site health consumer. It applies a five-second
deadline to subscription flushes because the process context has no deadline.
Do not expand
`eventfabric.Client` into a general message bus and do not implement the broad
pluggable distribution draft as part of this feature. A fake in-memory adapter
is sufficient for deterministic unit tests.

## Site view

Every instance creates the complete service list from the static descriptor
inventory before any message arrives. Each unit is keyed by:

```text
project / environment / site / machine / service
```

The service role is metadata, not part of the key. The key identifies the
deployed unit. A service with the same name on two machines is two units.

Each unit retains the newest fresh observation from every expected observer
role. The view derives one service status with the reducer in
[Contract and convergence](03-contract-and-convergence.md). Updates use one
mutex or a single owner goroutine. Query snapshots are immutable copies in the
descriptor inventory order, with observer roles sorted. The common inventory
keeps ordering deterministic across site instances.

No result is written to disk. A restarted process begins with every remote unit
Unknown and learns current state from repeated reports.

## Independence from ownership

The monitoring subsystem is process-scoped, not Active-site-scoped:

- Primary and Standby use identical local target configuration.
- Ownership transfer does not start, stop, reset, or change probe workers.
- Observations identify the fixed observer role, never Active or Passive as an
  authority claim.
- Service status does not affect the platform peer-health promotion gate.
- NATS route failure does not stop local lease renewal or promotion.

The API may include the observer's current Active or Passive state for
diagnostics, but reduction must not prefer the Active observer. Both observers
are independent witnesses of the same local target.

