// Package deploymentdescriptors verifies that the builder's and the platform's
// deployment descriptors describe the same contract.
//
// The descriptor is defined twice on purpose: the builder produces it
// (builder/deployment) and the platform consumes it (platform/deployment), and the
// platform must not depend on the build tool, so nothing at compile time keeps the
// two Go types in sync. The verification logic is ordinary package code: Run
// confirms the two types share a JSON shape and that a built descriptor round-trips
// through the platform's type.
//
// The conformance command (conformance/cmd) is what drives Run as this module's
// main check, both as a runnable program and through go test. This package's own
// tests are unit tests of its internal check logic in isolation.
package deploymentdescriptors
