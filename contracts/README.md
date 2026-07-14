# Contracts

Technology-neutral specifications that define interoperability requirements between independent modules.

Contracts are the authoritative description of public APIs, exchanged data formats, and compatibility expectations. They contain no shared implementation code and must not introduce production dependencies between modules.

Compliance is verified by the `conformance` module during the build process.

## Artifacts

- `openapi.yaml` - OpenAPI 3.0.3 specification of the platform's HTTP API. It is
  generated from the platform's API description (`platform/api`) by the
  `conformance` module, not edited by hand. The conformance golden test fails if
  this file drifts from the source. Regenerate it with:

  ```
  go test ./conformance -run TestOpenAPIContract -update
  ```

## Principles

- Language-independent
- Implementation-independent
- No runtime dependencies
- Versioned and backward-compatible where applicable
- Verified through automated conformance tests

## Consumers

- `platform`
- `sdk-dotnet`
- Future SDKs and integrations