// Package manifest defines the metadata files that ship inside a deployment
// package: the deployment manifest and the release metadata.
//
// The Manifest describes what the package is and how to deploy it (identity,
// role, binary, embedded descriptors, hosted services). The Release describes
// how the package was produced (builder, time, toolchain, target, binary
// checksum). Together they make a package self-describing and auditable on the
// customer premises without reference to the build environment.
package manifest
