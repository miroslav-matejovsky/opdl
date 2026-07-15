# Stage 1: Contract generation foundation

Estimate: 2-3 engineer-days.

## Goal

Extend the generated API pipeline so it can accurately describe the upcoming
registration operations without changing the current status endpoint yet. This
stage is tooling-only and must leave existing runtime behavior and SDK calls
working.

## Instructions

1. Extend `platform/api.Operation` with the minimum metadata needed by the two
   registration operations:

   - A stable OpenAPI `operationId`.
   - An optional JSON request body type and required flag.
   - More than one documented response status.
   - An optional JSON response body per status.

   Replace the current single `SuccessStatus` and `SuccessBody` fields only after
   adapting the existing status operation. Keep the API description independent
   of OpenAPI-specific structs.

2. Keep the current status operation represented by the expanded model. Add or
   update `platform/api` tests to prove its method, path, operation id, request
   absence, and response contract.

3. Extend `conformance/api-specifications` to generate:

   - `operationId` values.
   - Required `application/json` request bodies.
   - Multiple response status codes.
   - Top-level array response schemas.
   - Reusable component schemas for struct item types.
   - Integer constraints needed for Go `uint8` and `uint16`: minimum 0, maximum
     255 or 65535, and a suitable OpenAPI integer format when supported.
   - Optional nullable string fields without marking them required.

   Do not special-case registration type names in the generator. Derive behavior
   from the Go types and explicit operation metadata.

4. Update the compact Markdown renderer so each operation shows its request body
   before its responses. Array payloads must display the item schema and its
   fields. Multiple response statuses must remain deterministically sorted.

5. Add focused generator tests using synthetic request and response types. Cover:

   - A required request body.
   - A struct response.
   - A top-level array of structs.
   - Two success responses for one operation.
   - `uint8` and `uint16` bounds.
   - An optional string field.
   - Stable operation ordering and Markdown output.

6. Regenerate `api-specifications/openapi.yaml`,
   `api-specifications/openapi.md`, and the .NET client. At this stage the
   generated public API must still contain only the existing status endpoint.
   This can be done via conformance module.

7. Update `api-specifications/README.md` if its description of supported request
   or response shapes is no longer accurate.

8. Run focused Go tests for `platform/api` and
   `conformance/api-specifications`, then run `task all`.

## Acceptance

- The generator can represent the complete registration contract without
  hand-editing OpenAPI.
- Top-level arrays and bounded unsigned integers are correct in generated YAML.
- Kiota generation still produces a compiling SDK.
- Existing status behavior remains unchanged.
- `task all` passes and generated artifacts have no unexplained diff.

## Risks and controls

- Reflection can create unnamed schemas for slices. Reference the named element
  schema and use an inline array wrapper.
- Nullable and optional are different concepts in OpenAPI. Model the absent role
  consistently in Go, OpenAPI, generated C#, and JSON tests.
- Kiota method names depend on `operationId`. Add snapshot-like assertions for
  operation ids so accidental SDK source breaks are visible.
