# Step 70 - Migrate every event producer

Effort: 1-2 days

Complexity: High

## Goal

Replace `operations.Recorder` and `eventfabric.Publisher` usage with the one
producer-facing `events.Publisher`.

## Changes

1. Inject `events.Publisher` explicitly into application, redundancy,
   registration, Event Fabric, and NATS components that state events. Remove the
   recorder service locator from context.

2. Change every event emission to `Publish(ctx, event)`. Producers continue to
   construct only their package-owned event struct. Factory metadata and backend
   behavior remain invisible.

3. Apply one error policy:

   - domain operations propagate publication failure;
   - startup and shutdown paths return or join publication failure where their
     signatures allow it;
   - asynchronous callbacks report failures as ordinary diagnostics when they
     cannot return them;
   - no call silently discards an error;
   - no failure diagnostic is republished through the same pipeline.

4. Remove the producer-facing Event Fabric receipt and sequence dependency:

   - registration command publication returns only its stable proposal ID;
   - remove `Sequence` from `registration.ProposalReceipt` and
     `api.ProposalAccepted`;
   - regenerate OpenAPI and the .NET SDK after the API change;
   - keep sequences in projections and Event Fabric deliveries because domain
     conflict ordering still depends on the journal order.

5. Replace the ready-event receipt wait. After publishing readiness, query the
   Event Fabric high-water mark and wait until the local projection has applied
   that mark. This keeps backend-specific coordination in application
   composition rather than the producer API.

6. Update registration, HTTP, application, redundancy, and NATS tests to use a
   small fake `events.Publisher` or a fake `storage.Backend`, depending on the
   boundary under test.

7. Keep event structs in each owning package's `events.go`. Do not move event
   catalogs into `events`, `storage`, or composition.

## Tests

- Domain services know only `events.Publisher`.
- Domain publication failures retain their existing externally visible error
  behavior.
- Successful registration returns a proposal ID without a journal sequence.
- Registration conflict ordering still uses delivery sequence internally.
- Readiness waits until the ready event and all earlier events are projected.
- Lifecycle event publication failures are returned, joined, or diagnosed and
  never silently ignored.
- Causation and correlation still flow through handler contexts.

## Completion criteria

- There are no imports of `operations`.
- There are no references to the removed Event Fabric publisher, appender, or
  receipt.
- Generated OpenAPI and .NET SDK artifacts match the API model.
- Producer and API tests pass.

