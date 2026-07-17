# Stage 2: OPDL Event Fabric

## Outcome

Implement the OPDL-specific event boundary and its NATS JetStream adapter. The
adapter proves durable publication, replay, routing, redelivery, catch-up, and
shutdown without exposing NATS concepts to domain packages.

Complexity: High.

Estimated time: 4-6 engineering days.

Depends on: [Stage 1](01-event-model.md).

## Work

1. Add `platform/internal/eventfabric` with small consumer-side interfaces:
   - a publisher that appends an OPDL event and returns a receipt;
   - a projector runner that replays a selected journal in order and continues
     with live events;
   - a handler runner for durable per-service, per-node reactions;
   - lifecycle and health operations owned by runtime composition.
   Use these initial operations as the design boundary:
   - `Publisher.Publish(ctx, events.Event) (Receipt, error)`;
   - `Fabric.RunProjector(ctx, Projector) error`;
   - `Fabric.RunHandler(ctx, Handler) error`;
   - `Fabric.HighWater(ctx) (uint64, error)`;
   - `Fabric.State(ctx) (State, error)` and `Fabric.Close(ctx) error`.
   Exact Go signatures may change during Stage 1, but no additional generic
   messaging capability should be added without a current OPDL consumer.
2. Put `Receipt`, `Delivery`, route construction, envelope encoding, error
   values, and adapter contract tests in `platform/internal/eventfabric`. Keep
   projection reducers and service handlers outside the adapter. Event
   Fabric owns validation, encoding, routes, acknowledgement, retries, and
   delivery metadata. Domain code owns event meaning.
3. Add `platform/internal/eventfabric/nats` using `nats.go` and the linked
   `nats-server/v2/server` package with JetStream file storage. Pin both versions
   in `platform/go.mod`.
4. Derive server name, cluster name, peer routes, and journal scope from the
   deployment descriptor. Use client port `4222`, cluster route port `6222`, and
   monitoring port `8222` as adapter constants. Runtime overrides may move these
   sockets and the data directory for local scenarios but must not change
   deployment identity.
5. Select JetStream storage nodes from the full site membership sorted by
   machine name: one storage node for sites smaller than three machines and the
   first three for larger sites. Enable JetStream only on those nodes. All other
   embedded servers run Core NATS and join the same routes.
6. Add `Config` validation before opening sockets. Require a writable data
   directory on storage nodes, positive startup/catch-up/shutdown durations, unique addresses,
   and no self or duplicate route. Keep production routes derived; permit an
   explicit route override only in tests and local scenarios.
7. Create or validate the site journal idempotently at startup. Configure file
   storage, `LimitsPolicy`, `DiscardNew`, no maximum age, no maximum message
   count, a required maximum byte size, and a required maximum message size.
   Select replicas from site size using the Stage 1 rule. Refuse startup
   when an existing journal has incompatible subjects, storage, retention,
   limits, or replicas.
8. Publish synchronously through JetStream, never Core NATS, and set
   `Nats-Msg-Id` from the event or stable decision
   identity. Return the JetStream stream sequence in the OPDL receipt.
9. Run one node-wide projection runner on an ordered consumer from the first
   retained event. Keep it running after catch-up and dispatch deliveries to
   registered domain projectors in journal order. Name durable pull consumers
   `<site-scope>_<machine-token>_<service>_v1`, where `machine-token` uses the
   same stable safe-token function as routes. Filter them by Event Fabric routes
   and use explicit acknowledgement for handlers.
10. Bound acknowledgement wait, redelivery, connection, catch-up, drain, and
   shutdown behavior in configuration. Surface exhausted delivery and decode
   failures as readiness errors rather than dropping an event.
11. Return typed errors for closed fabric, invalid event, incompatible journal,
    catch-up timeout, and exhausted handler delivery. Wrap NATS errors with the
    OPDL operation, route, stream, or consumer name.
12. Report `platform.event_fabric.ready` and
    `platform.event_fabric.stopping` through the same journal. Do not define a
    `stopped` event: a closed transport cannot durably state its own completed
    close. Do not retain a separate JSONL event path as a second source of truth.
13. Add contract and integration tests against real embedded NATS servers. Put
    reusable server setup in `_test.go` files inside the NATS adapter. Do
    not add an in-memory transport adapter. Unit tests may call pure handlers and
    projectors directly with event values.

## Suggested package shape

```text
platform/internal/events/             event envelope and domain event contract
platform/internal/eventfabric/        OPDL Event Fabric contract and routing
platform/internal/eventfabric/nats/   NATS and JetStream adapter
```

The exact interfaces should stay narrower than a generic message bus. Do not
expose raw subjects, arbitrary headers, queue groups, key-value buckets, object
stores, or direct NATS connections.

## Initial configuration shape

```toml
[event_fabric.nats]
data_dir = "data/nats"
client_address = "10.0.1.10:4222"
cluster_address = "10.0.1.10:6222"
monitor_address = "127.0.0.1:8222"
max_bytes = 1073741824
max_message_bytes = 1048576
startup_timeout = "30s"
catch_up_timeout = "30s"
shutdown_timeout = "10s"
username = "opdl-site"
password_file = "secrets/nats-password"
```

Addresses may be omitted when they can be derived from the descriptor and fixed
ports. `data_dir` is required on selected storage nodes. Limits, durations, and
credentials for non-loopback addresses are required on every node. Read the
password from a file. Do not place credentials in the deployment descriptor or
startup summary.

## Tests

- A durable publish returns a receipt and the event can be replayed.
- Route construction isolates two sites with otherwise identical events.
- A restarted projector rebuilds the same state from the first retained event.
- A durable node handler receives events published while that node is offline.
- A two-node site runs one JetStream storage node and one Core NATS-only node;
  both can publish while the storage node is available.
- Redelivery after a missing acknowledgement invokes an idempotent handler.
- An invalid event remains visible and prevents readiness.
- Startup rejects an incompatible existing journal.
- Cancellation drains active handlers and closes NATS within the configured
  timeout.

## Exit criteria

- Event Fabric contract tests pass using only the NATS adapter.
- Domain-facing interfaces contain no NATS types or names.
- A two-process test publishes on one node and replays on another.
- NATS unavailability produces a clear startup or publish error.
- No correctness test requires the old shared-memory fabric adapter.

## Open questions and recommendations

- Linked server or supervised child process?
  Recommendation: link `nats-server/v2/server` into the platform. It keeps one
  artifact and makes startup and shutdown ownership explicit. Revisit when the
  broker needs an independent release or resource boundary.
- Which NATS versions should be pinned?
  Recommendation: at Stage 2 start, select the newest stable `nats-server/v2`
  and `nats.go` versions that pass the supported Go toolchain, pin exact module
  versions, and record them in package documentation. Do not use floating tool
  downloads or pre-release versions.
- Which ports belong in the deployment contract?
  Recommendation: keep peer IPs, not ports, in the descriptor. Use adapter
  constants `4222`, `6222`, and `8222`. Allow address overrides in runtime
  configuration for local scenarios. Bind monitoring to loopback by default.
- What initial journal limits should be used?
  Recommendation: require `max_bytes` and `max_message_bytes`. Use 1 GiB and
  1 MiB in the example POC configuration, with unlimited age and count plus
  `DiscardNew`. Adjust from observed event volume before field deployment.
- Is authentication required?
  Recommendation: require a shared site username and secret-file password when
  any NATS listener or route is non-loopback. Allow no authentication only for
  loopback tests. Add TLS later if traffic leaves a trusted site network.

## Risks

- Embedding a NATS server couples platform and journal lifecycle. A platform
  crash also removes one NATS member.
- Incorrect peer or replica configuration can prevent JetStream metadata quorum
  and block the whole site.
- Opening the HTTP API before replay reaches its high-water mark can expose
  incomplete projections.
- A generic abstraction would leak NATS into every domain and make replacement
  or testing difficult. Keep only the capabilities used by OPDL.
- Unbounded replay, redelivery, or shutdown can hang startup and deployment.
  Every wait needs context cancellation and a configured bound.
