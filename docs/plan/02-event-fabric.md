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
2. Keep projection reducers and service handlers outside the adapter. Event
   Fabric owns validation, encoding, routes, acknowledgement, retries, and
   delivery metadata. Domain code owns event meaning.
3. Add `platform/internal/eventfabric/nats` using the official Go client and an
   embedded NATS server with JetStream file storage.
4. Derive server identity, cluster identity, peers, routes, and journal scope
   from the deployment descriptor. Runtime overrides may move sockets and data
   directories but must not change deployment identity.
5. Create or validate the site journal idempotently at startup. Refuse startup
   when an existing journal has incompatible subjects, storage, retention,
   limits, or replicas.
6. Publish synchronously and set `Nats-Msg-Id` from the event or stable decision
   identity. Return the JetStream stream sequence in the OPDL receipt.
7. Use ordered delivery for local projector replay. Use durable pull consumers
   with explicit acknowledgement for service handlers.
8. Bound acknowledgement wait, redelivery, connection, catch-up, drain, and
   shutdown behavior in configuration. Surface exhausted delivery and decode
   failures as readiness errors rather than dropping an event.
9. Report Event Fabric lifecycle facts through the same journal after it is
   ready. Do not retain a separate JSONL event path as a second source of truth.
10. Add contract and integration tests against real embedded NATS servers. Do
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

## Tests

- A durable publish returns a receipt and the event can be replayed.
- Route construction isolates two sites with otherwise identical events.
- A restarted projector rebuilds the same state from the first retained event.
- A durable node handler receives events published while that node is offline.
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

## Open questions

- Will the embedded server use the same process lifecycle as the platform or a
  supervised child process? A linked server is simpler for one deployable
  binary, while a child process gives stronger failure isolation.
- Which NATS server and Go client versions should be pinned?
- Which route and cluster ports should be fixed in deployment descriptors, and
  which may be overridden for scenarios?
- What are the initial journal byte, message, age, and message-size limits?
- Is transport authentication required in the POC, or will site network
  isolation be an explicit temporary constraint?

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

