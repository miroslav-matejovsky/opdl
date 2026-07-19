# `utils/jsonfields`

## Functionality

Describe the fields a Go struct exposes through JSON: encoded name, Go type,
omission options, and exclusion. This gives reflection-based tools one trusted
interpretation instead of each parsing `json` tags independently.

## Extraction source and consumers

- Consolidate `jsonField` in
  `conformance-tests/api-specifications/openapi.go` and `jsonName` in
  `conformance-tests/deployment-descriptors/descriptors.go`.
- OpenAPI schema construction remains in `api-specifications`.
- Recursive wire-shape signatures and builder/platform comparison remain in
  `deployment-descriptors`.
- The utility must not import either descriptor type or any OpenAPI model.

## Package boundary

Accept a `reflect.Type` for a struct and return ordered field metadata or an
error. Match `encoding/json` rules for exported fields, empty tag names,
`json:"-"`, `omitempty`, anonymous fields, and conflicting promoted names. Keep
field discovery separate from schema or signature rendering.

Fail clearly for an unsupported construct rather than silently describing a
different wire shape from `encoding/json`. Preserve declaration order in the
metadata. Consumers may sort when their output contract requires it.

## Tests and documentation

- Tagged, untagged, renamed, skipped, and `omitempty` fields.
- Unexported fields and empty tag names.
- Pointer, anonymous, embedded, and conflicting promoted fields.
- Tags with unrelated options such as `string`.
- Compare discovered names with actual `encoding/json` output for representative
  values.
- Keep OpenAPI schema and descriptor signature tests as consumer regressions.

## Refactoring steps

1. Create `utils/jsonfields` and pin its relationship to `encoding/json` with
   table-driven tests.
2. Replace OpenAPI field discovery without moving schema decisions.
3. Replace descriptor signature field discovery without moving comparison
   decisions.
4. Delete both private tag parsers.

## Completion criteria

Both conformance packages use one tested field-discovery implementation,
generated contracts do not change, the utility contains no OPDL or OpenAPI
types, and `task all` passes.
