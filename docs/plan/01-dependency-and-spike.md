# Stage 1: dependency and spike

## Goal

Add huma to the `platform` module and prove, with throwaway code, that both paths
this plan depends on work: registering an operation over the standard-library
adapter, and dumping the OpenAPI document to bytes. No production wiring yet.

## Why first

Everything else assumes huma builds against Go 1.26.5, that `humago` binds to
`http.ServeMux`, and that `api.OpenAPI().DowngradeYAML()` produces a 3.0.3 spec
Kiota can consume. Prove those cheaply before touching the real contract.

## Changes

### 1. Add the dependency

In `platform/`:

```
go get github.com/danielgtaylor/huma/v2@latest
```

This pulls the module and the `adapters/humago` subpackage. Expected version at
time of writing: v2.39.0 or newer. Run `task tidy` so `platform/go.mod` and
`platform/go.sum` update, and confirm `go.work.sum` at the repo root picks it up.

### 2. Register the vendor in arch-lint

`platform/.go-arch-lint.yml` uses `depOnAnyVendor: false`, so an unlisted
dependency fails `task arch`. Add huma as a vendor and let `api` use it:

```yaml
vendors:
  # existing entries...
  huma: { in: "github.com/danielgtaylor/huma/v2**" }

deps:
  api:
    canUse: [huma]
  # httpapi is unchanged: mayDependOn: [api, registration]; it does not import huma.
```

The `api` component has no `deps` entry today (it is a leaf); add the one above.
The `**` glob covers the `adapters/humago` subpackage too. `internal/httpapi`
does not import huma, so it needs no huma entry.

If `conformance-tests` has its own arch-lint that enforces vendors
(`conformance-tests/.go-arch-lint.yml` or the `conformance-tests/architecture`
setup), add the same huma vendor there, since stage 4 makes conformance import
`platform/api` which pulls huma in transitively.

### 3. Spike (throwaway)

Write a scratch `main` or a `_test.go` under `platform/api` (its new home, see
stage 3) that:

- builds a config, disables the generated endpoints, and creates an API over a
  `http.ServeMux`;
- registers one trivial operation;
- calls `DowngradeYAML()` and prints or asserts the output is non-empty 3.0.3
  YAML.

```go
package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

func spike() ([]byte, error) {
	mux := http.NewServeMux()
	cfg := huma.DefaultConfig("OPDL Platform API", "0.1.0")
	cfg.OpenAPIPath = ""
	cfg.DocsPath = ""
	cfg.SchemasPath = ""
	hapi := humago.New(mux, cfg)

	type pingOutput struct {
		Body struct {
			Message string `json:"message" example:"pong"`
		}
	}
	huma.Register(hapi, huma.Operation{
		OperationID: "ping",
		Method:      http.MethodGet,
		Path:        "/ping",
		Summary:     "Health probe",
	}, func(ctx context.Context, _ *struct{}) (*pingOutput, error) {
		out := &pingOutput{}
		out.Body.Message = "pong"
		return out, nil
	})

	return hapi.OpenAPI().DowngradeYAML()
}
```

Field paths confirmed against v2.39.0: `Config.OpenAPIPath`, `Config.DocsPath`,
`Config.SchemasPath` are on `Config`; the description is
`cfg.OpenAPI.Info.Description` (`Config` has a named `OpenAPI *huma.OpenAPI`
field). `DefaultConfig` defaults the three paths to `/openapi`, `/docs`,
`/schemas`.

## Verify

- `task tidy && task vet && task arch` pass.
- The spike prints a document that starts with `openapi: 3.0.3`.
- Feed the spiked YAML to Kiota once by hand (`kiota generate ... --openapi
  <file>`) to confirm huma's downgraded output is accepted. This de-risks stage 4
  before any real contract depends on it.

## Out of scope

- Real operations, real DTOs, runtime wiring, spec-file writing. Delete the spike
  before or during stage 3.

## Exit criteria

huma builds in the module, arch-lint accepts it, and both the generation call and
Kiota round-trip are proven on throwaway input.

## Spike results (executed 2026-07-20, huma v2.39.0)

Done. All exit criteria met. The spike lived in `platform/api/spike_test.go` and
was deleted when stage 3 landed the real code.

Proven:

- huma v2.39.0 builds on Go 1.26.5; `humago.New` binds to a stdlib
  `http.ServeMux`; `hapi.OpenAPI().DowngradeYAML()` returns valid OpenAPI 3.0.3.
- `kiota generate --language CSharp` consumed the downgraded YAML and generated a
  client with no errors. The closed `role` set became a C# enum
  (`SpikeRequest_role.cs`), which auto-closes the enum item in
  `docs/backlog/api-contract.md`.

Findings that shape the later stages (decisions now settled):

1. **`$schema` injection.** `DefaultConfig` registers a schema-link create hook
   that adds a `$schema` property to every model. Kiota warns and generates a
   spurious `$schema` field. Setting `cfg.CreateHooks = nil` removes it cleanly
   (3 occurrences to 0). `Config()` in stage 3 clears `CreateHooks`.
2. **Integer bounds.** huma emits `minimum: 0` for unsigned ints but no maximum.
   To keep the current `maximum: 255` / `65535`, stage 2 adds explicit
   `maximum:"..."` tags. `uint8`/`uint16` get `format: int32`, `uint64` gets
   `format: int64, minimum: 0`.
3. **Key ordering.** huma marshals YAML keys alphabetically (`components`,
   `info`, `openapi`, `paths`), unlike the current file's `openapi`-first order.
   It is deterministic, so the regenerate-and-diff conformance gate still holds;
   the stage 4 diff is large but stable.
4. **Error responses.** Every operation gets `422` (validation) and `500` added
   automatically, and errors use `application/problem+json` with the RFC 9457
   `ErrorModel`. This settles the stage 3 error-model decision toward the huma
   default: it is what the framework produces and Kiota handles it.
5. **Strictness.** Every object schema gets `additionalProperties: false`. This
   is stricter than today and is kept.

Not blocking, informational: Kiota warns `format: uri` is unsupported and falls
back to string, and that no `servers` entry is present (the current spec has none
either, so this matches today).
