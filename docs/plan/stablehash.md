# `utils/stablehash`

## Functionality

Compute a deterministic SHA-256 digest for an ordered sequence of strings. Frame
each string with a fixed-width big-endian byte length before hashing its bytes so
field boundaries cannot collide.

## Extraction source and consumers

- Consolidate `writeLengthPrefixed` in
  `platform/internal/eventfabric/route.go` and `writeField` in
  `platform/internal/registration/catalog.go`.
- Event Fabric keeps base32 encoding and transport-safe casing.
- Registration keeps schema-version fields, field ordering, sorting of expected
  machines, and hexadecimal encoding.
- Builder file checksums remain separate because they hash a byte stream, not a
  framed sequence of semantic values.

## Package boundary

Expose a function equivalent to `Sum256(values ...string) [32]byte`. The package
owns only the framing algorithm and SHA-256 selection. Consumers own value
normalization, ordering, version fields, and text encoding of the result.

The framing algorithm is a compatibility contract. Document byte length rather
than rune count, the eight-byte big-endian length prefix, ordered input, and the
difference between no values and one empty value. Do not silently change the
algorithm in a later refactor.

## Tests and documentation

- Pin known digest vectors.
- Prove common boundary-forging pairs produce different digests.
- Cover empty input, empty strings, non-ASCII strings, ordering, and repeated
  values.
- Keep Event Fabric safe-token tests and registration ID tests as consumer
  regression tests with their current expected behavior.
- Add a test that the two former framing implementations produce the same bytes
  for representative inputs before deleting them.

## Refactoring steps

1. Create and test `utils/stablehash`.
2. Replace Event Fabric framing while leaving its base32 output local.
3. Replace registration framing while leaving its domain canonicalization local.
4. Remove the two private framing helpers.

## Completion criteria

Existing route tokens and registration IDs do not change, the algorithm is
fully documented and pinned by vectors, the utility has no OPDL imports, and
`task all` passes.
