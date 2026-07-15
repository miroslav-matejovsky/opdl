# Stage 2: Local registration vertical slice

Estimate: 5-7 engineer-days.

## Goal

Replace the mock status endpoint with a two-phase registration request, request
status, and registration-process list on one platform process. Complete the
public contract, generated .NET SDK, unit tests, and single-machine scenario in
the same stage so the repository remains green.

## Instructions

1. Add separate public request and response models in `platform/api`:

   ```text
   RegistrationRequest
     unit_type: uint8, required
     unit_id: uint16, required
     unit_type_name_advertised: string, required
     role: optional string, Master or Slave

   Registration
     unit_type: uint8, required
     unit_id: uint16, required
     unit_type_name_advertised: string, required
     role: optional string, Master or Slave
     machine: string, required
     ip: string, required
     status: string, pending or accepted or rejected
     reason: optional string
     platform_instances: array of PlatformInstanceRegistrationStatus, required

   PlatformInstanceRegistrationStatus
     machine: string, required
     ip: string, required
     status: string, pending or accepted or rejected
     reason: optional string
   ```

   Use a pointer or equivalent representation for optional role. Define exported
   role and status constants. The registration-level machine/IP identify the
   origin. `platform_instances` contains one deterministic entry per expected
   site platform instance. These fields exist only on server response models, so
   generated clients cannot set them in a valid request. `reason` is a bounded
   machine-readable code, not raw backend text. Document every exported type,
   field, and constant.

2. Add contract operations:

   - `POST /registrations`, operation id `registerUnit`.
   - `GET /registrations/{unit_type}/{unit_id}/status`, operation id
     `getRegistrationStatus`.
   - `GET /registrations`, operation id `listRegistrations`.
   - `POST` request body is one `RegistrationRequest`.
   - `POST` documents `202`, `400`, `409`, and `500` and has no success body.
   - Status documents `200`, `404`, and `500` and returns one `Registration` view
     on success.
   - `GET` documents `200` and `500`.
   - Successful list returns an array of all registration request views,
     including pending and rejected.

   Introduce one small JSON error model only if needed to keep error responses
   consistent. Do not build a general error framework.

3. Remove the status model and `GET /` operation from `platform/api`. Regenerate
   both API specification files in the same change. Confirm there is no mock
   endpoint left in `openapi.yaml` or `openapi.md`.

4. Create a focused registration package under `platform/internal`. It should
   own:

   - The composite key type used by both POST and status lookup.
   - A registration location value containing machine and IP.
   - Input validation.
   - Deterministic ordering.
   - Separate pending request, confirmation, and accepted registration records.
   - Projection of those records into one registration view with overall and
     per-instance states.
   - A coordinator interface defined next to the service that consumes it.
   - In-memory request, confirmation, and accepted-registration storage for this
     stage and unit tests.
   - Results that distinguish new request, idempotent retry, conflict, pending,
     accepted, rejected, and not found.

   The registration service accepts a validated request plus a trusted location
   injected at construction. The composite key contains only `unit_type` and
   `unit_id`. Creation must atomically refuse an existing pending or accepted key
   unless advertised name, role, machine, and IP all match exactly. Exact match is
   an idempotent retry. Any mismatch is conflict; registration is not an update.

   Keep public wire types in `platform/api`. Avoid making HTTP handlers or future
   distribution code responsible for domain rules.

5. Validate requests before changing state:

   - Reject malformed JSON, multiple JSON documents, and unknown fields.
   - Require `Content-Type: application/json` for `POST`.
   - Reject a blank advertised name.
   - Accept an absent role.
   - Reject any present role other than exact `Master` or `Slave`.
   - Reject client attempts to send `machine`, `ip`, or any other server-owned
     field as unknown request properties.
   - Rely on JSON decoding and the concrete Go types for numeric range checks,
     and test out-of-range and negative numbers.

6. Refactor `platform/internal/httpapi.NewHandler` to accept its dependencies.
   Register exact routes for `/registrations`; do not use `/` as a catch-all.
   Implement:

   - `POST` request creation with `202 Accepted` for a new request or exact retry.
   - `409 Conflict` with a stable machine-readable error code when the key exists
     with any different advertised name, role, machine, or IP.
   - Status lookup by bounded path parameters. It is available only on the
     request's origin machine; return 404 on another machine.
   - List with deterministic ordering and `[]` only when no request exists.
   - `Content-Type: application/json` on every JSON response.
   - `Allow` and `405 Method Not Allowed` for unsupported methods.
   - Context propagation into store calls.

   Return errors with context. Do not log in the handler unless the runtime can
   act on the log.

7. Wire one in-memory registration store into `platform/cmd/main.go`. Read
   `Machine` and `IP` from `cfg.Descriptor()` and inject that immutable location
   into the registration service. Do not derive registration IP from
   `cfg.Address()`, forwarded headers, or the remote client address. Construct
   dependencies before starting the HTTP server and fail startup if the embedded
   machine or IP is invalid.

8. Add a single-instance coordinator. A valid POST persists a pending request
   before returning. The coordinator validates it as the only expected platform
   instance, writes its self-confirmation, and promotes it to accepted. Keep the
   trigger injectable so tests can observe pending before allowing acceptance.
   List projects all requests, including pending and rejected. Status returns the
   same projected view for this origin machine.

9. Add table-driven tests for the registration package and HTTP handler. Cover:

   - Empty list.
   - POST returns 202 while a controllable request remains pending.
   - Status transitions from pending to accepted after self-confirmation.
   - List contains the request with overall pending before approval and accepted
     afterward.
   - The single local `platform_instances` entry changes from pending to accepted.
   - A rejected request remains visible with overall rejected, rejecting instance,
     and bounded reason.
   - Exact retry of pending and accepted requests.
   - Stored and returned machine and IP match the injected descriptor location.
   - A request cannot spoof machine or IP.
   - Different advertised name or role conflicts on the same machine.
   - Different injected machine or IP conflicts and leaves existing state
     unchanged.
   - A byte-for-byte identical second caller is treated as an idempotent retry.
   - Same `unit_id` under different `unit_type` values.
   - Deterministic registration sort by machine, unit type, and unit id, plus
     deterministic platform-instance sort by machine.
   - Optional role round-trip.
   - Both valid roles.
   - Every validation failure.
   - Unsupported methods and store failures.
   - Concurrent requests for the same key, checked with the race detector where
     practical. Exactly one distinct payload wins and every different payload
     receives conflict.
   - Status on a machine other than the origin returns 404.

10. Regenerate the .NET client from the new OpenAPI contract. Never hand-edit
   files under `sdk-dotnet/src/Opdl.Sdk/Client`.

11. Replace `PlatformStatusTests.cs` with registration behavior tests using the
    generated client. Update `sdk-dotnet/README.md` layout and usage example.
    Verify POST returns without a registered response, status polling uses the
    composite key path, both status and list return the same generated
    `Registration` view, origin and platform-instance machine/IP fields are
    server-derived, all three states map correctly, optional role maps correctly,
    and cancellation tokens propagate.

12. Update the existing single-machine Go scenario. It must expect an empty list,
    POST a request, observe it as pending in both status and list, poll status on
    the same machine until accepted, then verify list shows accepted. Assert
    origin and platform-instance machine/IP match the embedded descriptor. Remove
    every assertion that refers to the mock status response.

13. Run focused Go tests, regenerate artifacts, build the .NET solution, and run
    `task all`.

## Acceptance

- The only public runtime routes are request creation, origin-machine request
  status, and registration-process list.
- POST returning 202 never claims the unit is registered.
- One process proves pending, self-confirmation, accepted, exact retry, conflict,
  and deterministic process-list behavior.
- Clients cannot supply machine or IP; both values come from the embedded
  deployment descriptor.
- Every field is immutable after acceptance until future removal. Any mismatch
  for an existing key returns 409 and cannot alter pending or accepted state.
- The checked-in OpenAPI and .NET SDK match the served JSON.
- The single-machine black-box scenario uses registration request status, not the
  removed mock root-status endpoint.
- Authentication and authorization code is not introduced.
- `task all` passes.

## Risks and controls

- Map the typed registration-key conflict to 409 at the HTTP boundary. Do not
  infer a conflict by parsing error text.
- Do not let fast single-node confirmation collapse the API semantics. Tests must
  hold the coordinator so pending is observable before acceptance.
- Generated C# naming for `unit_id` may not match preferred acronym casing. Use
  generated names as-is in tests and documentation.
- The in-memory store is temporary runtime wiring, but remains useful as a fast
  unit-test implementation after distribution is added.
