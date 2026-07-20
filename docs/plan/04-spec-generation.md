# Stage 4: spec generation

## Goal

Generate `api-specifications/openapi.yaml` from the huma API instead of from the
reflection generator, keep it checked into git, and keep Kiota generating the
.NET SDK from it. Retire the bespoke generator.

## Current pipeline (what this replaces)

- `conformance-tests/api-specifications/openapi.go` builds an `openAPIDoc` from
  `platform/api.Describe()` by reflection, marshals it to YAML, and writes
  `../../api-specifications/openapi.yaml`. It also renders `openapi.md`.
- `execution.go::Run` writes the spec, then calls Kiota (`dotnet.go`).
- `conformance-tests/cmd` drives `Run` as a program and via `go test`.

## Target pipeline

huma is the source of truth. The huma API is built once with stub services and
its OpenAPI document is dumped to bytes. To keep huma inside `platform`, add a
platform helper and have the conformance module call it.

### 1. Platform helper

In `platform/internal/httpapi`:

```go
// OpenAPIYAML builds the platform API surface and returns its OpenAPI 3.0.3
// document as YAML. Handlers are never invoked here, so nil services are safe:
// registration only reads each operation and its input/output types.
func OpenAPIYAML() ([]byte, error) {
	mux := http.NewServeMux()
	cfg := Config()
	cfg.OpenAPIPath = ""
	cfg.DocsPath = ""
	cfg.SchemasPath = ""
	hapi := humago.New(mux, cfg)
	Register(hapi, nil, nil)
	return hapi.OpenAPI().DowngradeYAML()
}
```

`Register` with `nil` services is safe because generation reads only the
`huma.Operation` values and the input/output struct types by reflection; the
handler closures are never called. If you prefer to avoid nil, pass zero-value
services, but nil is simplest and correct here.

Expose this through the public boundary if the conformance module cannot import
an `internal` package. `conformance-tests` is a separate module that already
imports `platform/api` (public). It cannot import `platform/internal/httpapi`.
So add a thin public entry point in the `platform/api` package or a new public
`platform/openapi` package that calls the internal helper:

```go
// platform/api/openapi.go (public, but now it may import httpapi... no).
```

Note the boundary: `platform/api` must stay dependency-free, so it cannot call
`httpapi`. Put the public entry point in a new public package, for example
`platform/openapi`, that is allowed to import `internal/httpapi`:

```go
// platform/openapi/openapi.go
package openapi

import "github.com/miroslav-matejovsky/opdl/platform/internal/httpapi"

// YAML returns the platform's OpenAPI 3.0.3 document.
func YAML() ([]byte, error) { return httpapi.OpenAPIYAML() }
```

Add a matching arch-lint component:

```yaml
components:
  openapi: { in: openapi }
deps:
  openapi:
    mayDependOn: [httpapi]
```

### 2. Rewrite the conformance generator

Replace the body of `conformance-tests/api-specifications/openapi.go`
`generateOpenAPISpec` with:

```go
func generateOpenAPISpec() error {
	yamlBytes, err := platformopenapi.YAML()
	if err != nil {
		return err
	}
	if err := atomicfile.WriteFile(contractPath, yamlBytes, 0o644); err != nil {
		return err
	}
	// openapi.md: see decision below.
	return nil
}
```

Delete the reflection machinery: `buildOpenAPIDoc`, `schemaBuilder`,
`inlineSchemaFor`, `schemaFor`, `integerSchema`, `structSchema`, and every
`openAPI*` type. `execution.go::Run` and `dotnet.go` (Kiota) stay as is; the
Kiota step still reads `contractPath`.

`conformance-tests/go.mod` gains a require on `platform` (already present) and
transitively on huma through the `platform/openapi` import. Run `task tidy`.

### 3. `openapi.md` decision

huma does not produce the markdown companion. Choose one:

- **Keep it (adapt):** parse the generated `openapi.yaml` (or take
  `api.OpenAPI()` as a `*huma.OpenAPI` value) and feed the existing renderer in
  `markdown.go`. This keeps the PR-review artifact described in
  `api-specifications/README.md`. Requires reworking `renderMarkdown` to read
  huma's model or the parsed YAML instead of `platformapi.Contract`.
- **Drop it (POC-simplest):** delete `markdown.go`, `markdown_test.go`,
  `markdownPath`, and `openapi.md`; update `api-specifications/README.md`. The
  YAML remains the reviewable artifact.

Recommended for the POC: drop it, and reintroduce a markdown view later if the
review workflow misses it. Record the choice in stage 6's doc updates.

## Decision: OpenAPI version

- **Recommended:** `DowngradeYAML()` emits 3.0.3, matching the current
  checked-in `openapi.yaml` header and the version Kiota already consumes.
- Alternative: `YAML()` emits 3.1. Kiota supports 3.1, but moving versions is
  extra churn with no POC benefit.

## Determinism

The conformance check regenerates and diffs (see
`api-specifications/README.md`). huma's document marshals through a stable
ordering, but confirm the output is byte-stable across runs:

- run `go run ./conformance-tests/cmd` twice and `git diff` must be empty on the
  second run;
- if any map ordering is unstable, sort at write time or pin it, so the
  "regenerate and diff" gate stays meaningful.

## Verify

- `go run ./conformance-tests/cmd` writes `api-specifications/openapi.yaml`; the
  diff versus the previous file is reviewed and intentional (field docs,
  examples, enums appear; structure otherwise matches).
- `go test ./conformance-tests/cmd` passes (it regenerates and expects no diff).
- Kiota regenerates `sdk-dotnet` from the new YAML and `task sdk-dotnet` passes.

## Exit criteria

`openapi.yaml` is generated from the huma API, the reflection generator is gone,
the spec is byte-stable, and the .NET SDK builds from the new spec.
