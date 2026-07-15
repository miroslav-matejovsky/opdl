# Stage 2: Local registration vertical slice

Estimate: 4-6 engineer-days.

## Goal

Replace the mock status endpoint with working registration and list operations
on one platform process. Complete the public contract, generated .NET SDK, unit
tests, and single-machine scenario in the same stage so the repository remains
green.

## Instructions

1. Add the public registration model in `platform/api`:

   ```text
   unit_type: uint8, required
   unit_id: uint16, required
   unit_type_name_advertised: string, required
   role: optional string, Master or Slave
   ```

   Use a pointer or equivalent representation for optional role. Define exported
   role constants. Document every exported type, field, and constant.

2. Add contract operations:

   - `POST /registrations`, operation id `registerUnit`.
   - `GET /registrations`, operation id `listRegistrations`.
   - `POST` request body is one registration.
   - `POST` documents `201`, `200`, `400`, and `500`.
   - `GET` documents `200` and `500`.
   - Successful `POST` responses return the accepted registration.
   - Successful `GET` returns an array of registrations.

   Introduce one small JSON error model only if needed to keep error responses
   consistent. Do not build a general error framework.

3. Remove the status model and `GET /` operation from `platform/api`. Regenerate
   both API specification files in the same change. Confirm there is no mock
   endpoint left in `openapi.yaml` or `openapi.md`.

4. Create a focused registration package under `platform/internal`. It should
   own:

   - The composite key type.
   - Input validation.
   - Deterministic ordering.
   - A `Store` interface defined next to the service that consumes it.
   - An in-memory store implementation for this stage and unit tests.
   - An upsert result that distinguishes created, changed, and unchanged.

   Keep public wire types in `platform/api`. Avoid making HTTP handlers or future
   distribution code responsible for domain rules.

5. Validate requests before changing state:

   - Reject malformed JSON, multiple JSON documents, and unknown fields.
   - Require `Content-Type: application/json` for `POST`.
   - Reject a blank advertised name.
   - Accept an absent role.
   - Reject any present role other than exact `Master` or `Slave`.
   - Rely on JSON decoding and the concrete Go types for numeric range checks,
     and test out-of-range and negative numbers.

6. Refactor `platform/internal/httpapi.NewHandler` to accept its dependencies.
   Register exact routes for `/registrations`; do not use `/` as a catch-all.
   Implement:

   - `POST` upsert with `201` for created and `200` for changed or unchanged.
   - `GET` with deterministic ordering and `[]` for an empty store.
   - `Content-Type: application/json` on every JSON response.
   - `Allow` and `405 Method Not Allowed` for unsupported methods.
   - Context propagation into store calls.

   Return errors with context. Do not log in the handler unless the runtime can
   act on the log.

7. Wire one in-memory registration store into `platform/cmd/main.go`. Construct
   dependencies before starting the HTTP server and fail startup if construction
   fails.

8. Add table-driven tests for the registration package and HTTP handler. Cover:

   - Empty list.
   - Create, exact retry, and metadata update.
   - Same `unit_id` under different `unit_type` values.
   - Deterministic sort order.
   - Optional role round-trip.
   - Both valid roles.
   - Every validation failure.
   - Unsupported methods and store failures.
   - Concurrent upserts of the same key, checked with the race detector where
     practical.

9. Regenerate the .NET client from the new OpenAPI contract. Never hand-edit
   files under `sdk-dotnet/src/Opdl.Sdk/Client`.

10. Replace `PlatformStatusTests.cs` with registration behavior tests using the
    generated client. Update `sdk-dotnet/README.md` layout and usage example.
    Verify create and list calls, field mapping, optional role, and cancellation
    token propagation.

11. Update the existing single-machine Go scenario. It must wait on
    `GET /registrations`, expect an empty JSON array, register a record, and list
    it. Remove every assertion that refers to the mock status response.

12. Run focused Go tests, regenerate artifacts, build the .NET solution, and run
    `task all`.

## Acceptance

- The only public runtime routes are the two registration operations.
- One process supports create, retry, update, and deterministic list behavior.
- The checked-in OpenAPI and .NET SDK match the served JSON.
- The single-machine black-box scenario uses registration behavior, not status.
- Authentication and authorization code is not introduced.
- `task all` passes.

## Risks and controls

- HTTP `POST` has two successful statuses. Test both served responses and both
  generated OpenAPI entries.
- Generated C# naming for `unit_id` may not match preferred acronym casing. Use
  generated names as-is in tests and documentation.
- The in-memory store is temporary runtime wiring, but remains useful as a fast
  unit-test implementation after distribution is added.
