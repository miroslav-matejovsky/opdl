# Stage 6: cleanup and verification

## Goal

Remove the code the huma pipeline made dead, update the documentation to describe
the new pipeline, and prove the whole workspace still passes.

## Delete dead code

- `platform/api/api.go`: remove the structural description types and function now
  that huma owns the spec:
  - `Describe`, `Contract`, `Operation`, `Response`, `RequestBody`,
    `PathParameter`.
  - Keep the DTOs (`RegistrationRequest`, `ProposalAccepted`, `Registration`,
    `RegistrationConflict`, `PlatformInstanceRegistrationStatus`) and the value
    constants (`RoleMaster`, `RoleSlave`, `RegistrationStatus*`,
    `RegistrationConflictResolutionResolved`).
  - `Error`: delete it if the RFC 9457 error model was chosen in stage 3; keep it
    if the `{code}` override was chosen.
- `conformance-tests/api-specifications/openapi.go`: the reflection generator is
  reduced to the write-and-Kiota flow in stage 4; delete any remaining
  `openAPI*` types, `schemaBuilder`, `integerSchema`, `structSchema`, `schemaFor`.
- `openapi_test.go`: drop tests of the deleted reflection helpers; keep or add a
  test asserting the generated YAML is 3.0.3 and contains the expected operations.
- If `openapi.md` was dropped in stage 4: delete `markdown.go`,
  `markdown_test.go`, and `api-specifications/openapi.md`.
- Check whether `utils/jsonfields` is still used after the reflection generator
  is gone. If the generator was its only consumer, note it in `.todo` for a
  follow-up removal (do not delete cross-module utils casually).

`task deadcode` will flag unreachable functions in cmd entry points; use it to
confirm nothing was left stranded. Per the memory note, scenario helper code must
stay in `_test.go` or `task deadcode` fails.

## Update documentation

- `platform/api/doc.go`: it currently says the package holds "a structural
  description of the operations it exposes" and that the conformance module
  "renders Describe into the OpenAPI specification." Rewrite: the package is the
  dependency-free contract DTOs, annotated with tags huma reads; the OpenAPI spec
  is generated from the huma operations in `internal/httpapi`.
- `platform/internal/httpapi/doc.go`: describe the huma-based handler and the
  `Register`/`NewServeMux`/`OpenAPIYAML` entry points.
- `platform/openapi/doc.go`: new package doc for the public spec entry point (if
  that package was added in stage 4).
- `api-specifications/README.md`: update the "Artifacts" section. `openapi.yaml`
  is now generated from the huma API (via `platform/openapi`), not from
  `platform/api.Describe()`. Update or remove the `openapi.md` bullet per the
  stage 4 decision.
- `conformance-tests/api-specifications/doc.go` and
  `conformance-tests/cmd/main.go` doc comment: they reference "the platform's API
  description (platform/api)"; change to "the platform's huma API".
- `docs/02-registration.md`: if it documents HTTP behavior (error codes, status
  codes), reconcile it with the error model chosen in stage 3.
- `docs/backlog/api-contract.md`: the "closed value sets are described as free
  strings" item is resolved by stage 2. Remove it or mark it done.
- Root `README.md` and `platform/README.md`: update if they describe the API
  layer or the generation pipeline.

## Full verification

Run the gate defined in `AGENTS.md`:

```
task all
```

This runs tidy, vet, fmt, deadcode, lint, arch, test, the .NET SDK build,
validate, and scenarios. All must pass. Specifically confirm:

- `task arch` accepts the new huma vendor and the `openapi` component.
- `task test` passes, including the rewritten `httpapi` and conformance tests.
- `task sdk-dotnet` builds the Kiota client from the regenerated `openapi.yaml`.
- `task scenarios` passes; re-run with `-count=1` after `platform/**` edits so the
  scenario cache does not hide platform changes.
- `git diff api-specifications/` shows only the intended contract change, and a
  second `go run ./conformance-tests/cmd` produces no further diff (determinism).

## Exit criteria

Dead description code is gone, docs describe the huma pipeline, `task all` passes,
and the generated spec plus the .NET SDK are byte-stable and build clean.
