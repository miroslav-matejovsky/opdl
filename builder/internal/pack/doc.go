// Package pack is the side-effecting stage of the builder: it produces a
// deployment package for a machine by driving the platform's own build.
//
// For each machine it stages that machine's deployment descriptor (as JSON)
// in a temporary directory, maps it over the platform's neutral embedded
// descriptor with Go's build overlay, runs go build on the platform command,
// and writes the resulting binary alongside a copy of the descriptor, a
// deployment manifest, release metadata, and a checksums file into the package
// directory. Every package is self-describing: it can be audited on the
// customer premises without reference to the build environment.
// The manifest declares the preferred primary launch directly and includes an
// optional standby launch when the descriptor's standby slot is not disabled.
// Both use the packaged binary and differ only by their explicit -instance
// argument. The decision is read from an explicit field rather than inferred
// from an omitted one, so a package's process set always matches what its
// blueprint stated.
//
// The overlay means a build never changes the working tree. Killing a builder
// cannot leave a customer descriptor in platform/embedded/deployment.json. The
// checked-in neutral descriptor therefore remains the source used by normal
// platform builds and task run.
package pack
