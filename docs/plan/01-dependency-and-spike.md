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
