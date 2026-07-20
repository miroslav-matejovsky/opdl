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

`platform/api.OpenAPIYAML()` (added in stage 3) is the single source. The
conformance module already imports `platform/api`, so it calls that function
directly and writes the file. huma is pulled in transitively; that is fine for
this internal module. No wrapper package.

### 1. Rewrite the conformance generator

Replace `generateOpenAPISpec` in
`conformance-tests/api-specifications/openapi.go`:

```go
import (
	platformapi "github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/utils/atomicfile"
)

func generateOpenAPISpec() error {
	yamlBytes, err := platformapi.OpenAPIYAML()
	if err != nil {
		return err
	}
	if err := atomicfile.WriteFile(contractPath, yamlBytes, 0o644); err != nil {
		return err
	}
	// openapi.md: see the decision below.
	return nil
}
```

Delete the reflection machinery: `buildOpenAPIDoc`, `schemaBuilder`,
`inlineSchemaFor`, `schemaFor`, `integerSchema`, `structSchema`, and every
`openAPI*` type. Keep `contractPath` (and `markdownPath` only if markdown stays).
`execution.go::Run` and `dotnet.go` (Kiota) are unchanged; Kiota still reads
`contractPath`.

Run `task tidy` so `conformance-tests/go.mod` records the transitive huma
requirement, and update `conformance-tests` arch-lint if it enforces vendors
(see stage 1).

### 2. `openapi.md` decision

huma does not produce the markdown companion. Choose one:

- **Keep it (adapt):** have `platform/api` also expose the document model, for
  example `func OpenAPIDocument() *huma.OpenAPI`, and rework `markdown.go`'s
  `renderMarkdown` to read that (or the parsed YAML) instead of
  `platformapi.Contract`. Preserves the PR-review artifact described in
  `api-specifications/README.md`.
- **Drop it (POC-simplest):** delete `markdown.go`, `markdown_test.go`,
  `markdownPath`, and `openapi.md`; update `api-specifications/README.md`. The
  YAML stays the reviewable artifact.

Recommended for the POC: drop it, and reintroduce a markdown view later if the
review workflow misses it. Record the choice in stage 6's doc updates.

## Decision: OpenAPI version

- **Recommended:** `DowngradeYAML()` emits 3.0.3, matching the current
  checked-in `openapi.yaml` header and the version Kiota already consumes. This
  is what `OpenAPIYAML()` uses in the stage 3 sketch.
- Alternative: switch `OpenAPIYAML()` to `YAML()` for 3.1. Kiota supports 3.1,
  but moving versions is extra churn with no POC benefit.

## Determinism

The conformance check regenerates and diffs (see
`api-specifications/README.md`). Confirm huma's output is byte-stable:

- run `go run ./conformance-tests/cmd` twice; the second run must leave
  `git diff` empty;
- if any ordering is unstable, pin it at write time so the "regenerate and diff"
  gate stays meaningful.

## Verify

- `go run ./conformance-tests/cmd` writes `api-specifications/openapi.yaml`; the
  diff versus the previous file is reviewed and intentional (field docs,
  examples, enums, and the RFC 9457 error schema appear; paths and schemas
  otherwise match today's).
- `go test ./conformance-tests/cmd` passes (regenerates and expects no diff).
- Kiota regenerates `sdk-dotnet` from the new YAML and `task sdk-dotnet` passes.

## Exit criteria

`openapi.yaml` is generated from `api.OpenAPIYAML()`, the reflection generator is
gone, the spec is byte-stable, and the .NET SDK builds from the new spec.
