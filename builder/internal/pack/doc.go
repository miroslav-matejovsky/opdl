// Package pack is the side-effecting stage of the builder: it produces a
// deployment package for a machine by driving the platform's own build.
//
// For each machine it stages that machine's deployment descriptor (as JSON)
// into the platform's embedded folder, runs go build on the platform command,
// and writes the resulting binary alongside a copy of the descriptor, a
// deployment manifest, release metadata, and a checksums file into the package
// directory. Every package is self-describing: it can be audited on the
// customer premises without reference to the build environment.
//
// Because every machine shares the one embedded descriptor file, builds are
// sequential: stage, compile, package, repeat. The placeholder descriptor
// present before any build is snapshotted on construction and rewritten by
// Restore, so the working tree is left exactly as it was found even if a build
// fails partway through. This is what upholds the rule that the repository
// always compiles without a customer descriptor.
package pack
