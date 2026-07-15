# Stage 3: Platform events

Estimate: 3-5 engineer-days.

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
   - Optional deterministic tags. Use a sorted, duplicate-free string list and
     omit it when empty.
   - Node identity from the embedded deployment descriptor: project,
     environment, site, machine, and role.
   - Typed event data encoded as JSON.

   Inject clock and id generation in tests. Do not use timing sleeps.

3. Add only these event types initially:

   - `platform.registration.requested` with the four client fields plus
     server-derived origin machine and IP.
   - `platform.registration.confirmed` with unit key, origin machine, and
     confirming machine. The internal proposal fingerprint is not exposed as a
     request identifier in the event.
   - `platform.registration.accepted` with all six committed registration fields
     after every expected platform instance confirmed.
   - `platform.registration.rejected`, tagged `warning`, with unit key, origin,
     rejecting machine, and bounded reason.
   - `platform.registration.conflict`, tagged `warning`, with `unit_type`,
     `unit_id`, stored or pending fields, attempted fields, and bounded reason
     `registration_key_conflict`.
   - `platform.fabric.started` with adapter name, local member address, and
     current member count. Stage 4 emits it.
   - `platform.fabric.stopped` with the adapter name. Stage 4 emits it.

   Exact retries do not emit another requested or accepted event. Failed input
   validation does not emit a registration event. A key conflict emits the
   warning event for every rejected occurrence but does not change request or
   accepted state.

4. Define a small recorder interface near the registration service. Inject it
   into the registration use case so the domain operation records a successful
   request creation, each platform confirmation, final acceptance, rejection,
   and conflict at their owning state transitions. On a typed key conflict,
   record the warning event before returning 409 to HTTP. Provide a no-op
   implementation for tests that do not inspect events.

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
   encoding, tag normalization, concurrent writes, and close behavior. Add
   registration tests that distinguish requested, confirmed, accepted, rejected,
   exact retry, key conflict, and validation failures by emitted events.

9. Add a small scenario-side JSONL reader and event matcher under `scenarios`.
   Keep it limited to polling a file for event type, node, and selected payload
   fields. Reuse concepts from `_opdl-v1/scenarios/eventlog`, not the complete old
   expectation or baseline framework.

10. Configure an events directory in the single-machine scenario. Register a
    unit and assert the ordered sequence `platform.registration.requested`,
    `platform.registration.confirmed`, and `platform.registration.accepted`.
    Envelope node, payload machine, and payload IP must match the embedded
    descriptor. Repeat the exact request and prove no duplicate phase event
    exists by reading the synchronously flushed file after the HTTP response. The
    scenario may then force-stop its child process on platforms where a graceful
    child interrupt is not portable. Keep graceful shutdown and close-order
    assertions in in-process lifecycle tests.

11. Document the event envelope, current catalog, storage behavior, and
    limitations in a platform-local document such as `platform/internal/events/doc.go`
    plus a concise architecture section in the root `README.md`.

12. Run focused event, config, registration, and scenario tests, then run
    `task all`.

## Acceptance

- Request, per-instance confirmation, and final acceptance produce deterministic
  ordered JSONL events.
- Exact retries do not produce duplicate phase events.
- Key conflicts produce a `warning`-tagged conflict event containing existing
  and attempted data while preserving state.
- Event records identify the deployment machine that accepted the request.
- Scenarios use events as behavioral evidence.
- In-process shutdown tests prove the HTTP server and event sink close cleanly.
- `task all` passes.

## Risks and controls

- Recording after the store write can fail after state changed. Return a 500 with
  context and document the event delivery as best effort for this phase. Do not
  attempt rollback across the store and file.
- Abrupt process termination may lose operating system buffers even after a Go
  flush. Scenarios should read synchronously flushed events while processes are
  live before force-stop cleanup.
- Event ids must not depend only on low-resolution timestamps. Combine time with
  process-local uniqueness or use a suitable standard identifier implementation.
