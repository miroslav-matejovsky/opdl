// Package apispecifications regenerates the platform's API specification contract
// and the artifacts derived from it, keeping them in sync with the code that owns
// them.
//
// The generation logic is ordinary package code, not test code: the OpenAPI
// specification (api-specifications/openapi.yaml) and its Markdown companion
// (api-specifications/openapi.md) are rendered from the platform's huma API
// (platform/api.OpenAPIYAML), and the .NET client (sdk-dotnet) is generated from
// the specification with Kiota. Run always rewrites all three, in that order -
// specification first, markdown companion second, client third, since the
// derived artifacts depend on the specification - rather than checking them for
// staleness, so the checked-in artifacts are always exactly what the current
// source produces.
//
// The conformance command (conformance-tests/cmd) is what drives Run as this module's
// main check, both as a runnable program and through go test. This package's own
// tests are unit tests of its internal check logic in isolation.
package apispecifications
