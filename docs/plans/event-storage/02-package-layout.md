# Package layout alternatives

## Option A: capability plus concrete adapter - recommended

```text
events/storage
events/storage/jsonl
events/storage/eventfabric
events/storage/nats
```

- `storage` owns `Backend` and fan-out.
- `jsonl.Backend` implements `storage.Backend`.
- `eventfabric` owns ordered replay, delivery, projection, handler, health, and
  sequence contracts. It has no concrete storage engine.
- `nats.Backend` implements both `storage.Backend` and `eventfabric.Fabric`.
- Runtime composition registers the NATS object once as a storage backend and
  also uses it through the Event Fabric interface.

The blueprint calls this logical backend `eventfabric` and selects `nats` as
its implementation. NATS is never enabled as an unrelated direct destination.
A future persistence-only destination implements `storage.Backend`. A future
Event Fabric adapter implements both `storage.Backend` and
`eventfabric.Fabric`, then replaces NATS in runtime composition.

### Pros

- No forwarding wrapper.
- No risk of storing the same event once through Event Fabric and again through
  NATS.
- Event producers see only `events.Publisher`.
- Event Fabric consumers see no NATS types.
- A future adapter can implement the same two interfaces without changing
  producers or projections.

### Cons

- `storage/eventfabric` contains capability contracts, not a concrete
  `Backend` type.
- The NATS type has both write-storage and richer fabric responsibilities.
- A future Event Fabric adapter must support the full fabric contract, not only
  storing an envelope.

## Option B: Event Fabric backend wrapping a NATS backend

```text
events/storage/eventfabric
events/storage/nats
```

- `eventfabric.Backend` implements `storage.Backend` and delegates writes to an
  inner backend.
- `nats.Backend` implements `storage.Backend` and the richer Event Fabric
  operations.
- Runtime composition wraps NATS with `eventfabric.Backend` and registers only
  the wrapper in fan-out. It never registers NATS independently.

### Pros

- Both proposed packages contain a type implementing the common interface.
- The configured destination is visibly Event Fabric with NATS underneath it.
- The wrapper could later add Event Fabric-wide validation or policy.

### Cons

- Both layers expose the same write operation, but only NATS performs it.
- The common interface is too small to express replay, delivery, health, and
  ordering, so the wrapper still needs another richer dependency.
- Registering both objects accidentally stores an event twice.
- There is no current shared policy that justifies the wrapper.

## Option C: Event Fabric backend wrapping a NATS driver

```text
events/storage/eventfabric
events/storage/eventfabric/nats
```

- `eventfabric.Backend` implements `storage.Backend`.
- A private Event Fabric driver interface includes append, replay, delivery,
  health, and lifecycle operations.
- `eventfabric/nats` implements that driver.

### Pros

- The concrete backend is named Event Fabric.
- NATS is visibly an implementation detail nested under it.
- Runtime composition registers only `eventfabric.Backend`.

### Cons

- The driver interface is almost as large as the current Fabric interface.
- The Event Fabric wrapper adds little behavior and mostly forwards calls.
- A generic driver contract may be shaped around NATS before a second adapter
  exists.
- It does not provide the requested sibling package `events/storage/nats`.

## Option D: Event Fabric and NATS as separate backends

```text
events/storage/eventfabric
events/storage/nats
```

Both types independently implement `storage.Backend` and both can be included in
fan-out.

### Pros

- Every named package contains a concrete backend.
- Configuration can list both destinations independently.

### Cons

- With NATS as the Event Fabric implementation, both destinations are the same
  physical journal.
- Enabling both stores every event twice or requires hidden deduplication.
- It makes NATS look usable without Event Fabric, contrary to the requirement.
- It gives future maintainers no clear ownership of routing, replay, health, or
  ordering behavior.

### Recommendation

Reject this option. Event Fabric and NATS are different abstraction levels, not
independent destinations.

## JSONL package proposal

Use `internal/events/storage/jsonl` with a concrete `Backend`.

It should own only:

- opening the derived event file;
- validating or encoding the canonical envelope with `events.Encode`;
- appending exactly one JSON object and newline per call;
- serializing concurrent writes;
- flushing and closing;
- contextual file errors.

It should not own event types, envelope creation, stderr logging, retry policy,
fan-out, or blueprint parsing. The current `operations` package can be removed
after its callers use the common publisher.
