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
// It also generates the platform's OpenAPI contract (contracts/openapi.yaml) from
// the platform's API description (platform/api) and fails if the checked-in file
// is stale, so the published specification tracks the code that serves it.
// Regenerate the contract with:
//
//	go test ./conformance -run TestOpenAPIContract -update
//
// It is a test-only module, shipping no runtime code, only the checks and
// generators that guard the contracts.
package conformance
