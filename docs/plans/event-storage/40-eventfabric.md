# Step 40 - Move Event Fabric contracts

Effort: 0.5-1 day

Complexity: Medium

## Goal

Move the abstract Event Fabric capability below event storage and remove its
producer-facing publication model.

## Changes

1. Move `platform/internal/eventfabric` to
   `platform/internal/events/storage/eventfabric`.

2. Keep the capability contracts in this package:

   - `Fabric`;
   - `Delivery`;
   - `Projector`;
   - `Handler`;
   - routes and site scope;
   - health, high-water, pending-work, lifecycle, and ordering types;
   - Event Fabric-owned event structs in `events.go`.

3. Make `eventfabric.Fabric` embed `storage.Backend` for its write side.

4. Remove `eventfabric.Publisher`, `Appender`, `NewPublisher`, and the public
   storage `Receipt`. Producers now use `events.Publisher`; adapters receive
   complete envelopes through `storage.Backend.Store`.

5. Keep journal sequence only on `eventfabric.Delivery`, high-water queries, and
   projection coordination. Never add it to `events.Envelope`.

6. Update domain packages to import the new Event Fabric path only for replay,
   delivery, projection, handler, or routing capabilities. They must not import
   NATS.

7. Update architecture dependency rules for the new package boundaries.

## Tests

- Route, scope, delivery, and error behavior is unchanged.
- Compile-time assertions show a Fabric is also a storage backend.
- No Event Fabric test needs a producer-facing receipt.
- Architecture checks prevent domain packages from importing NATS.

## Completion criteria

- The old `platform/internal/eventfabric` package is gone.
- `storage/eventfabric` contains no NATS imports.
- Event Fabric package tests and architecture checks pass.

