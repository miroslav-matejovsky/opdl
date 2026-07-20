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

## Current state (what this plan replaces)

| Concern | Today | After huma |
| --- | --- | --- |
| Contract DTOs | `platform/api` structs with `json` tags | Same structs, plus huma validation/doc tags |
| Operation description | `platform/api.Describe()` returns `Contract`/`Operation`/`Response` | `huma.Register` calls in `platform/internal/httpapi` |
| HTTP handlers | `net/http` `ServeMux` in `internal/httpapi` | huma operations over `humago` (`http.ServeMux`) |
| OpenAPI generation | `conformance-tests/api-specifications/openapi.go` (reflection) | `api.OpenAPI().DowngradeYAML()` from the huma API |
| `openapi.yaml` | OpenAPI 3.0.3, checked in | OpenAPI 3.0.3 (downgraded from huma 3.1), checked in |
| `openapi.md` | custom markdown renderer | decided in stage 4 (keep, adapt, or drop) |
| `.NET` SDK | Kiota from `openapi.yaml` | unchanged: Kiota from `openapi.yaml` |

## Target architecture

- `platform/api` stays the dependency-free contract package. huma reads struct
  tags, which are plain strings, so the DTOs gain `doc`/`example`/`enum` tags
  without importing huma. The structural `Describe`/`Contract`/`Operation` types
  are removed.
- `platform/internal/httpapi` owns the huma operations. A single `Register`
  function attaches every operation to a `huma.API`. Both the runtime server and
  the spec generator call it, so the served contract and the generated spec can
  never drift.
- The huma dependency lives only inside `platform` (in `httpapi`, plus the
  `humago` adapter). The `conformance-tests` module calls a platform helper that
  returns the OpenAPI bytes, so huma does not leak into the conformance module.
- Exposing `/openapi.yaml` and `/docs` on the running server is a one-line config
  toggle. The spec is still written to `api-specifications/openapi.yaml` in git,
  which is the authoritative artifact. HTTP exposure is optional and off by
  default unless we choose otherwise in stage 5.

## Key facts about huma (verified against v2.39.0, 2026-07-15)

- Import path `github.com/danielgtaylor/huma/v2`. Requires Go 1.25+. This repo is
  on Go 1.26.5, so no toolchain change is needed.
- Standard-library adapter: `github.com/danielgtaylor/huma/v2/adapters/humago`.
  `humago.New(mux, config)` binds a huma API to a Go 1.22+ `http.ServeMux`.
- `huma.Register[I, O](api, huma.Operation{...}, handler)` registers one
  operation. Input/output are structs; request body is an `I.Body` field, path
  params use a `path:"name"` tag, response body is an `O.Body` field.
- Success status: `huma.Operation.DefaultStatus` (for example 202).
- Errors: `huma.Error400BadRequest(...)`, `huma.Error404NotFound(...)`,
  `huma.Error503ServiceUnavailable(...)`, `huma.NewError(status, msg)`. Default
  error body is RFC 9457 problem+json. The global error model can be overridden
  by replacing `huma.NewError`.
- Spec export: `api.OpenAPI().YAML()` (3.1) and `api.OpenAPI().DowngradeYAML()`
  (3.0.3). Both return `([]byte, error)`.
- Generated endpoints are controlled by `Config.OpenAPIPath`, `Config.DocsPath`,
  `Config.SchemasPath`. Setting a path to `""` disables that endpoint.

## Decisions to make (flagged in the stages)

1. **Error model.** huma defaults to RFC 9457 problem+json
   (`{status,title,detail,errors}`); today the API returns `{"code": "..."}` with
   stable codes (`invalid_request`, `registration_not_found`,
   `journal_unavailable`, `internal_error`). Recommended for the POC: adopt
   huma's RFC 9457 default and carry the stable code in `detail`. Alternative:
   override `huma.NewError` to keep the `{code}` shape. Decided in stage 3.
2. **OpenAPI version.** Emit 3.0.3 via `DowngradeYAML()` to keep the checked-in
   spec on the same version Kiota already consumes, or move to 3.1 with
   `YAML()`. Recommended: stay on 3.0.3 for the POC. Decided in stage 4.
3. **`openapi.md` companion.** Keep the human-readable markdown (adapt the
   renderer to huma's `*huma.OpenAPI` model), or drop it. Decided in stage 4.
4. **HTTP exposure of the spec.** Keep `/openapi.yaml` and `/docs` off, or turn
   them on. Recommended: off by default; trivially reversible. Decided in stage 5.

## Stages

Each stage is one file and one reviewable change. Run `task all` at the end of
each stage that touches code (per `AGENTS.md`).

1. [Dependency and spike](01-dependency-and-spike.md) - add huma, prove the
   generation and serving paths compile and run.
2. [Annotate contract types](02-annotate-contract-types.md) - add huma tags to
   `platform/api` DTOs; keep the package dependency-free.
3. [Register operations in httpapi](03-register-operations-httpapi.md) - replace
   the `ServeMux` handler with huma operations behind one `Register` function.
4. [Spec generation](04-spec-generation.md) - generate `openapi.yaml` from the
   huma API; retire the reflection generator; keep Kiota.
5. [Runtime wiring](05-runtime-wiring.md) - serve the huma API from the runtime;
   decide optional `/openapi` and `/docs` exposure.
6. [Cleanup and verification](06-cleanup-and-verification.md) - delete dead
   contract-description code, update docs, verify diffs and the SDK build.
