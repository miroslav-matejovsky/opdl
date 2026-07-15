# Stage 3: Platform events

Estimate: 3-4 engineer-days.

## Goal

Add the smallest reusable platform event mechanism needed to observe
registration behavior and verify it in black-box scenarios.

## Instructions

1. Create `platform/internal/events` with package documentation. Base the design
   on the useful event envelope concepts in `_opdl-v1/platform/internal/events`,
   but implement only what this use case needs.

2. Define a self-contained JSON event record with:

   - Unique event id.
   - Stable dotted event type.
   - Monotonic sequence within one process run.
   - UTC `occurred_at` timestamp.
   - Source subsystem.
   - Node identity from the embedded deployment descriptor: project,
     environment, site, machine, and role.
   - Typed event data encoded as JSON.

   Inject clock and id generation in tests. Do not use timing sleeps.

3. Add only these event types initially:

   - `platform.registration.created` with all four registration fields.
   - `platform.registration.updated` with the accepted new registration fields.
   - `platform.distribution.started` with the local member address and current
     member count. Stage 4 emits it.
   - `platform.distribution.stopped` with no payload. Stage 4 emits it.

   Exact retries do not emit a change event. Failed validation does not emit a
   registration event.

4. Define a small recorder interface near the registration service. Inject it
   into the registration use case so the domain operation records a successful
   state change after the store accepts it. Provide a no-op implementation for
   tests that do not inspect events.

5. Add a JSONL sink under `platform/internal/events/jsonl`:

   - Create the configured directory.
   - Append one compact JSON object per line to `events.jsonl`.
   - Flush each accepted record before returning so a scenario can observe it.
   - Serialize writes safely.
   - Return append and close errors with file context.
   - Do not add rotation, retention, asynchronous queues, metrics, or an HTTP
     reader in this stage.

6. Extend `platform/config.json` and `platform/internal/config` with optional
   `events_dir`. Empty means the no-op recorder. Validate a configured path by
   constructing the sink during startup, not on the first event.

7. Update runtime composition:

   - Build the event recorder after configuration is loaded.
   - Inject node identity and the recorder into registration handling.
   - Close the recorder during orderly shutdown.
   - Refactor the current direct `ListenAndServe` call to support signal-driven
     server shutdown and dependency cleanup with bounded contexts.
   - Preserve original shutdown and sink errors with context.

8. Add deterministic unit tests for event envelope stamping, sequence, JSONL
   encoding, concurrent writes, and close behavior. Add registration tests that
   distinguish created, updated, unchanged, and failed operations by emitted
   events.

9. Add a small scenario-side JSONL reader and event matcher under `scenarios`.
   Keep it limited to polling a file for event type, node, and selected payload
   fields. Reuse concepts from `_opdl-v1/scenarios/eventlog`, not the complete old
   expectation or baseline framework.

10. Configure an events directory in the single-machine scenario. Register a
    unit, wait for `platform.registration.created`, and assert its node envelope
    and payload. Update the same unit and assert one ordered
    `platform.registration.updated`. Repeat the exact update and prove no third
    registration change event exists by reading the synchronously flushed file
    after the HTTP response. The scenario may then force-stop its child process
    on platforms where a graceful child interrupt is not portable. Keep graceful
    shutdown and close-order assertions in in-process lifecycle tests.

11. Document the event envelope, current catalog, storage behavior, and
    limitations in a platform-local document such as `platform/internal/events/doc.go`
    plus a concise architecture section in the root `README.md`.

12. Run focused event, config, registration, and scenario tests, then run
    `task all`.

## Acceptance

- Registration creates and actual updates produce deterministic JSONL events.
- Exact retries and rejected requests do not produce false change events.
- Event records identify the deployment machine that accepted the request.
- Scenarios use events as behavioral evidence.
- In-process shutdown tests prove the HTTP server and event sink close cleanly.
- `task all` passes.

## Risks and controls

- Recording after the store write can fail after state changed. Return a 500 with
  context and document the event delivery as best effort for this phase. Do not
  attempt rollback across the store and file.
- Abrupt process termination may lose operating system buffers even after a Go
  flush. Scenarios should request orderly shutdown before final exact-count
  assertions.
- Event ids must not depend only on low-resolution timestamps. Combine time with
  process-local uniqueness or use a suitable standard identifier implementation.
