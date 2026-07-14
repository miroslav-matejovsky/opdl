// Package conformance verifies that the independent representations of a shared
// contract stay compatible across the workspace's modules, and generates the
// specification artifacts under contracts/ from the code that owns them.
//
// The deployment descriptor is defined twice on purpose: the builder produces it
// (builder/deployment) and the platform consumes it (platform/deployment), and
// the platform must not depend on the build tool. Nothing at compile time keeps
// the two types in sync. This module's tests do: they import both and fail if the
// descriptors diverge in JSON shape or stop round-tripping.
//
// It also generates the downstream contract artifacts and fails if they go stale,
// so the published specification and the SDKs track the code that serves them:
//
//   - contracts/openapi.yaml, the OpenAPI specification, from the platform's API
//     description (platform/api).
//   - sdk-dotnet, the .NET client, generated from that OpenAPI specification with
//     Kiota (this check needs Kiota installed; it skips otherwise).
//
// Regenerate every artifact with:
//
//	go test ./conformance -update
//
// It is a test-only module, shipping no runtime code, only the checks and
// generators that guard the contracts.
package conformance
