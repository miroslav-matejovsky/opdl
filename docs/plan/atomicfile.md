# `utils/atomicfile`

## Functionality

Write a complete byte slice to a sibling temporary file and replace the target
atomically. Readers must observe either the previous complete file or the next
complete file, never a partial write.

## Extraction source and consumers

- Move the replacement and Windows sharing-violation retry behavior from
  `platform/internal/redundancy/status.go` and `status_replace_*.go`.
- Keep JSON marshaling and the `redundancy.Status` schema in `platform`.
- Adopt the same primitive for complete generated or packaged files in
  `builder/internal/pack` and `conformance-tests/api-specifications` where
  atomic visibility is useful. The utility must not know JSON, YAML, OpenAPI, or
  OPDL file names.

## Package boundary

Expose one small operation equivalent to `WriteFile(path, data, mode) error`.
Use a unique sibling temporary file, apply the requested permissions, close it,
replace the target, and remove the temporary file after failure. Document that
atomic visibility is guaranteed only when the temporary and target are on the
same filesystem. State explicitly whether the function guarantees disk
durability; do not imply `fsync` durability unless it is implemented.

Windows replacement may retry only errors known to represent a short-lived
sharing conflict. The retry must be bounded. Other errors must return
immediately with the path and operation in the error.

## Tests and documentation

- Round-trip bytes and requested permissions where the operating system exposes
  them.
- Replace an existing file and verify no partial content is observable under
  concurrent reads and repeated writes.
- Verify Windows replacement succeeds after a reader releases its handle.
- Cover write, close, replace, and cleanup failures where they can be induced
  deterministically.
- Verify concurrent writers use distinct temporary paths and leave no temporary
  files after completion.
- Preserve `redundancy.Status` round-trip tests as consumer contract tests.

## Refactoring steps

1. Create and fully test `utils/atomicfile`.
2. Change status writing to marshal locally and call `atomicfile.WriteFile`.
3. Remove `status_replace_*.go` after all supported operating systems use the
   utility.
4. Migrate builder and conformance outputs separately. Do not combine those
   consumer changes with the initial status extraction.

## Completion criteria

The utility has no OPDL imports, status readers never observe partial JSON, error
messages retain the failing path and operation, no temporary files leak, and
`task all` passes.
