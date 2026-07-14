# Contracts

Technology-neutral specifications that define interoperability requirements between independent modules.

Contracts are the authoritative description of public APIs, exchanged data formats, and compatibility expectations. They contain no shared implementation code and must not introduce production dependencies between modules.

Compliance is verified by the `conformance` module during the build process.

## Principles

- Language-independent
- Implementation-independent
- No runtime dependencies
- Versioned and backward-compatible where applicable
- Verified through automated conformance tests

## Consumers

- `platform`
- `sdk-dotnet`
- `sdk-go`
- Future SDKs and integrations