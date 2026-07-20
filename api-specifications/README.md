# API specifications

Technology-neutral specifications that define the platform's public API for
independent consumers.

An API specification is the authoritative description of the platform's HTTP API:
the shape SDKs decode and integrators build against. It contains no shared
implementation code and must not introduce production dependencies between modules.

Compliance is verified by the `conformance-tests` module during the build process.

## Artifacts

- `openapi.yaml` - OpenAPI 3.0.3 specification of the platform's HTTP API. It is
  rendered from the platform's huma API (`platform/api.OpenAPIYAML`) by the
  `conformance-tests` module, not edited by hand, and the .NET SDK (`sdk-dotnet`)
  is generated from it in turn. huma emits OpenAPI 3.1 natively; the artifact is
  the 3.0.3 downgrade the SDK toolchain (Kiota) consumes.

  The conformance check always regenerates it from the current source rather than
  checking it for staleness, so a change that was not propagated here shows up as
  an unexpected diff after running:

  ```
  go test ./conformance-tests/cmd
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
