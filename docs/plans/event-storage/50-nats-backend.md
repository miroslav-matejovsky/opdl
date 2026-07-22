# Step 50 - Move and adapt the NATS backend

Effort: 1-2 days

Complexity: High

## Goal

Make NATS the current concrete implementation of both event storage and Event
Fabric without adding an Event Fabric forwarding backend.

## Changes

1. Move `platform/internal/eventfabric/nats` to
   `platform/internal/events/storage/nats`.

2. Rename the main concrete type to `Backend`. Add compile-time assertions that
   it implements both `storage.Backend` and `eventfabric.Fabric`.

3. Replace `Append` with `Store`. Validate and encode the supplied envelope,
   publish it through JetStream, wait for acknowledgement, and return only an
   error.

4. Keep NATS deduplication behavior based on stable identity or occurrence ID.
   When JetStream reports a duplicate, retain the existing verification that the
   stored event is the same event. The verification no longer escapes as a
   receipt.

5. Rename NATS configuration fields from generic `DataDir` to
   `JetStreamStoreDir` and source them only from the resolved blueprint field.

6. Keep delivery sequence inside `eventfabric.Delivery`. NATS acknowledgement
   sequences remain adapter internals unless used by Event Fabric high-water or
   delivery behavior.

7. Preserve replay, projector, handler, reconnect, readiness, journal
   validation, deduplication, and shutdown behavior.

8. Prevent recursive publication. `Backend.Store` must never publish another
   event when its own write fails. It returns a contextual error.

9. Handle NATS lifecycle events with two-phase composition:

   - construct the NATS backend before it is connected;
   - include that object in the fan-out publisher;
   - start the backend with the publisher available for lifecycle events;
   - before JetStream is ready, lifecycle events can succeed in JSONL and fail
     in the not-yet-ready NATS backend;
   - report that partial publication as an ordinary startup diagnostic without
     recursively creating another event;
   - after readiness, all NATS lifecycle events use the full fan-out publisher.

   This is the explicit bootstrap exception to full multi-backend success. A
   backend cannot store facts about its own unavailability while unavailable.

10. Make `Backend.Close` emit no events through the fan-out publisher. Runtime
    composition publishes the stopping fact while every backend is still open.
    A result known only after NATS has stopped is a shutdown diagnostic, not an
    event that falsely claims it was stored in every configured backend.

11. Keep every remaining NATS-owned event struct in the moved package's
    `events.go`. Remove lifecycle events that cannot satisfy the accepted
    storage semantics.

## Tests

- `Store` retains and replays the exact supplied envelope.
- Invalid envelopes are rejected before NATS publication.
- Stable-identity and occurrence-ID deduplication remain correct.
- Replay, projector, handler, redelivery, pending count, health, and high-water
  tests pass at the new package path.
- `jetstream_store_dir` is passed unchanged to the embedded server.
- Store before ready and after close returns stable state errors.
- A Store failure emits no recursive event.
- Startup lifecycle events always reach JSONL and reach NATS after readiness.

## Completion criteria

- Only this package imports NATS client or server libraries.
- There is no separate concrete Event Fabric backend around NATS.
- NATS integration tests pass from the new package.
