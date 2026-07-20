# Plan: first-class OpenAPI with huma

Introduce [huma](https://huma.rocks/) ([github.com/danielgtaylor/huma](https://github.com/danielgtaylor/huma))
as the platform's HTTP API layer, so the OpenAPI specification is generated from
the code that serves the API instead of from a hand-rolled description.

This is a POC. Backwards compatibility is not a goal. Favor the simplest correct
design (see `AGENTS.md`).

## Why huma

The platform already treats the HTTP contract as generated, not hand-written:
`platform/api.Describe()` returns a structural `Contract`, and
`conformance-tests/api-specifications/openapi.go` walks it by reflection to emit
`api-specifications/openapi.yaml`, which Kiota then turns into `sdk-dotnet`.

That reflection generator is bespoke code the project maintains: its own
`openAPIDoc` types, schema derivation, integer bounds, enum handling (none yet),
and markdown renderer. huma replaces the whole "describe the API, then reflect
it into OpenAPI" pipeline with one source: the operation handlers themselves.
The spec is derived from the Go request and response types huma serves.

## Design choice: huma lives in `platform/api`

huma is an explicit dependency of the public `platform/api` package. That package
owns the DTOs, the huma operations, the huma config, and the spec export. The
`conformance-tests` module imports `platform/api` and calls its export function
directly to write `openapi.yaml`. This is deliberate and pragmatic:

- No separate wrapper package, no "keep `api` dependency-free" rule to work
  around, and no import-cycle gymnastics.
- One source of truth: the same `api.Register` call that serves requests is what
  the spec is generated from.
- Leaking huma into `conformance-tests` is fine: it is an internal build/verify
  module, not a shipped artifact.

An import cycle is still avoided cleanly. `platform/api` must not import
`internal/registration` (registration already imports `api`). So `api` does not
reference the service types directly. Instead it declares a small `Handlers`
struct of function fields that the runtime fills from the registration services.
`api` depends on huma; it does not depend on registration.

## Current state (what this plan replaces)

| Concern | Today | After huma |
| --- | --- | --- |
| Contract DTOs | `platform/api` structs with `json` tags | Same structs, plus huma validation/doc tags |
| Operation description | `platform/api.Describe()` returns `Contract`/`Operation`/`Response` | `huma.Register` calls in `platform/api` |
| HTTP handlers | `net/http` `ServeMux` in `internal/httpapi` | huma operations over `humago`, built in `platform/api` |
| Service wiring | `httpapi.NewHandler(commands, queries)` | `httpapi.NewHandler` fills `api.Handlers` from the services |
| OpenAPI generation | `conformance-tests/api-specifications/openapi.go` (reflection) | `api.OpenAPIYAML()`, called by conformance |
| `openapi.yaml` | OpenAPI 3.0.3, checked in | OpenAPI 3.0.3 (downgraded from huma 3.1), checked in |
| `openapi.md` | custom markdown renderer | decided in stage 4 (keep, adapt, or drop) |
| `.NET` SDK | Kiota from `openapi.yaml` | unchanged: Kiota from `openapi.yaml` |

## Target architecture

- `platform/api` (public) owns:
  - the DTOs, annotated with huma tags (`doc`, `example`, `enum`, bounds);
  - `Handlers`, a struct of function fields the runtime supplies;
  - `Register(hapi, Handlers)` registering every operation;
  - `Config()` for the huma config (title, version, description);
  - `NewServeMux(Handlers, exposeSpec)` returning the served `http.Handler`;
  - `OpenAPIYAML()` returning the OpenAPI 3.0.3 document as bytes;
  - `ErrJournalUnavailable`, the sentinel that maps to 503 (moved here from
    `registration`, since the status mapping is part of the contract).
  - Imports: `net/http`, `github.com/danielgtaylor/huma/v2`, and
    `.../huma/v2/adapters/humago`.
- `platform/internal/httpapi` becomes a thin wiring layer: `NewHandler(commands,
  queries, exposeSpec)` builds `api.Handlers` closures from the registration
  services and calls `api.NewServeMux`. It imports `api` and `registration`, not
  huma.
- `platform/internal/app/runtime.go` still calls `httpapi.NewHandler`; only the
  signature (an `exposeSpec` bool) changes.
- `conformance-tests` calls `api.OpenAPIYAML()` and writes the file. huma comes
  in transitively; that is acceptable for this internal module.
- Exposing `/openapi` and `/docs` on the running server is a bool passed to
  `NewServeMux`. The authoritative artifact is `api-specifications/openapi.yaml`
  in git; HTTP exposure is optional and off by default unless chosen in stage 5.

## Key facts about huma (verified against v2.39.0, 2026-07-15)

- Import path `github.com/danielgtaylor/huma/v2`. Requires Go 1.25+. This repo is
  on Go 1.26.5, so no toolchain change is needed.
- Standard-library adapter: `github.com/danielgtaylor/huma/v2/adapters/humago`.
  `humago.New(mux, config)` binds a huma API to a Go 1.22+ `http.ServeMux`.
- `huma.Register[I, O](api, huma.Operation{...}, handler)` registers one
  operation. Request body is an `I.Body` field, path params use a `path:"name"`
  tag, response body is an `O.Body` field. Success status is
  `huma.Operation.DefaultStatus`; documented error codes go in
  `huma.Operation.Errors []int`.
- Errors: `huma.Error400BadRequest(...)`, `huma.Error404NotFound(...)`,
  `huma.Error500InternalServerError(...)`, `huma.Error503ServiceUnavailable(...)`.
  Default error body is RFC 9457 problem+json; the global model can be overridden
  by replacing `huma.NewError`.
- Spec export: `api.OpenAPI().YAML()` (3.1) and `api.OpenAPI().DowngradeYAML()`
  (3.0.3). Both return `([]byte, error)`.
- `huma.DefaultConfig(title, version)` sets `Config.OpenAPI.Info.Title/Version`
  and defaults `Config.OpenAPIPath="/openapi"`, `DocsPath="/docs"`,
  `SchemasPath="/schemas"`. Set a path to `""` to disable that endpoint. Set the
  description via `cfg.OpenAPI.Info.Description`.

## Decisions (resolved during implementation)

1. **Error model: RFC 9457 problem+json (huma default).** The stable machine
   codes (`invalid_request`, `registration_not_found`, `journal_unavailable`,
   `internal_error`) are carried in `detail`. Kiota generates `ErrorModel`
   (extends `ApiException`); the one `sdk-dotnet` E2E error assertion was updated
   from the old `Error.Code` to `ErrorModel.Detail`.
2. **OpenAPI version: 3.0.3 via `DowngradeYAML()`.** Matches what Kiota already
   consumes.
3. **`openapi.md` companion: dropped.** The renderer read the deleted
   `Contract` model; the YAML is the reviewable artifact. Can return later.
4. **HTTP exposure of the spec: off by default.** `httpapi.NewHandler(..., false)`.
   Flip the bool to serve huma's `/openapi`, `/docs`, `/schemas`.
5. **Closed value sets (enums): deferred, kept as free strings.** Adding `enum`
   tags works (verified: Kiota emits C# enums), but it retypes the SDK surface
   and the E2E string comparisons, which is unrelated to introducing huma. Left
   in `docs/backlog/api-contract.md`.
6. **`$schema` injection: removed** via `cfg.CreateHooks = nil` in `api.Config`,
   so models and the SDK stay clean.

## Implementation notes

- huma marshals OpenAPI keys alphabetically, so the regenerated
  `api-specifications/openapi.yaml` reorders (components, info, openapi, paths)
  and is otherwise a full contract refresh. It is deterministic, so the
  regenerate-and-diff conformance gate still holds.
- huma adds `422` (validation) and, for GET operations without declared errors, a
  `default` error response. Schema violations (out-of-range int, unknown field,
  wrong type) are `422`; domain rejections (blank name, unknown role) stay `400`.
- `ErrJournalUnavailable` moved from `internal/registration` to `platform/api`.
- `platform/api` now depends on huma (arch-lint vendor `huma`, `api canUse`).
  `internal/httpapi` is thin wiring and imports no huma.

## Stages

Each stage is one file and one reviewable change. Run `task all` at the end of
each stage that touches code (per `AGENTS.md`).

1. [Dependency and spike](01-dependency-and-spike.md) - add huma, prove the
   generation and serving paths compile and run.
2. [Annotate contract types](02-annotate-contract-types.md) - add huma tags to
   `platform/api` DTOs.
3. [Register operations](03-register-operations.md) - add the huma operations,
   `Handlers`, `Register`, `Config`, `NewServeMux`, and `OpenAPIYAML` to
   `platform/api`; reduce `internal/httpapi` to wiring.
4. [Spec generation](04-spec-generation.md) - generate `openapi.yaml` from
   `api.OpenAPIYAML()`; retire the reflection generator; keep Kiota.
5. [Runtime wiring](05-runtime-wiring.md) - serve the huma API from the runtime;
   decide optional `/openapi` and `/docs` exposure.
6. [Cleanup and verification](06-cleanup-and-verification.md) - delete dead
   contract-description code, update docs, verify diffs and the SDK build.
