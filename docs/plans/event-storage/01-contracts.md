# Producer and storage contracts

## Producer-facing contract

Producers should depend only on the canonical event package:

```go
type Publisher interface {
	Publish(context.Context, Event) error
}
```

The implementation uses the process `events.Factory` to create one envelope,
then gives that same envelope to every backend. Producers do not create an
envelope, serialize JSON, choose a backend, or supply origin and timing data.

### Pros

- Business code depends only on the event model.
- Every backend receives the same event ID, timestamp, origin, and payload.
- Tests can replace the publisher with a small fake.
- Adding a backend does not change producers.

### Cons

- The current Event Fabric receipt and journal sequence disappear from the
  producer contract.
- Code that waits for a journal sequence needs an Event Fabric-specific
  coordination API outside business publication.
- The registration API currently exposes the journal sequence and must either
  stop exposing it or obtain progress through a separate query.

### Recommendation

Use this narrow contract. A journal sequence is a position assigned by one
backend, not part of a domain event or a portable publication result. Keep
projection progress and readiness waits inside the Event Fabric boundary.

If a publication identifier is required by a caller, return a small canonical
result containing only the envelope ID. Do not return backend names, positions,
or a map of backend receipts.

## Storage contract

Use package `internal/events/storage` and name the interface `Backend`:

```go
type Backend interface {
	Store(context.Context, events.Envelope) error
	Close(context.Context) error
}
```

`Store` accepts an already stamped envelope. A backend must not create or change
event metadata. A nil result means that backend accepted the envelope according
to its durability contract.

`Close` belongs on the interface because JSONL must flush and close its file,
while a remote or embedded backend may need bounded shutdown. It also lets the
composition root close a configured list without type assertions.

### Naming alternatives

#### `events/storage.Backend` - recommended

Pros:

- `storage` clearly describes the package purpose.
- `Backend` clearly describes one implementation inside that package.
- It reads naturally as `storage.Backend`, `jsonl.Backend`, and `nats.Backend`.

Cons:

- Event Fabric also distributes and replays events, so storage is not its whole
  responsibility.

#### `events/backend.Storage`

Pros:

- Emphasizes pluggability.

Cons:

- `backend.Storage` is less direct than `storage.Backend`.
- A package named only `backend` does not say what is behind it.

#### `events/storage.Store`

Pros:

- Short and familiar.

Cons:

- `Store.Store(...)` is awkward.
- `Store` can be confused with a data container instead of a destination.

## Rejected contract shapes

Do not pass `events.Event` to each backend. That would stamp and serialize the
same publication more than once, producing different envelopes.

Do not add backend configuration, names, health, replay, or query methods to the
common interface. Those capabilities differ by backend. Event Fabric should
expose them through its own richer interface.

Do not let a backend publish diagnostic events through the same publisher while
handling `Store`. A failing backend would recursively publish its own failure.
It should return a contextual error instead.

