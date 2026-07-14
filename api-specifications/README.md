# API specifications

Technology-neutral specifications that define the platform's public API for
independent consumers.

An API specification is the authoritative description of the platform's HTTP API:
the shape SDKs decode and integrators build against. It contains no shared
implementation code and must not introduce production dependencies between modules.

Compliance is verified by the `conformance` module during the build process.

## Artifacts

- `openapi.yaml` - OpenAPI 3.0.3 specification of the platform's HTTP API. It is
  generated from the platform's API description (`platform/api`) by the
  `conformance` module, not edited by hand, and the .NET SDK (`sdk-dotnet`) is
  generated from it in turn.
- `openapi.md` - a compact, human-readable companion to `openapi.yaml`: one
  section per operation, its response payload's field table inlined directly
  beneath it, meant to be skimmed in a PR diff when reviewing an API change.
  Generated from the same source alongside `openapi.yaml`, not edited by hand.

  The conformance check always regenerates both from the current source rather
  than checking them for staleness, so a change that was not propagated here shows
  up as an unexpected diff after running:

  ```
  go test ./conformance/cmd
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
