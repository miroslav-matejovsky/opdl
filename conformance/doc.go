// Package conformance verifies that the independent representations of a shared
// contract stay compatible across the workspace's modules.
//
// The deployment descriptor is defined twice on purpose: the builder produces it
// (builder/deployment) and the platform consumes it (platform/deployment), and
// the platform must not depend on the build tool. Nothing at compile time keeps
// the two types in sync. This module's tests do: they import both and fail if the
// descriptors diverge in JSON shape or stop round-tripping. It is a test-only
// module, shipping no runtime code, only the checks that guard the contracts.
package conformance
