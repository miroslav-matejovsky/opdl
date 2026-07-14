// Package pack is the side-effecting stage of the builder: it produces a
// deployment package for a machine by driving the platform's own build.
//
// For each machine it runs go build on the platform command and writes the
// resulting binary into the package directory alongside the machine's deployment
// descriptor (as JSON), a deployment manifest, release metadata, and a checksums
// file. Every package is self-describing: it can be audited on the customer
// premises without reference to the build environment.
//
// The old builder customized each binary by staging the machine's descriptor
// into the platform's embedded folder before compiling, then restoring a
// placeholder so the working tree stayed clean. The platform does not yet read
// an embedded descriptor (it is just a simple main), so that staging step is not
// implemented here: the descriptor ships beside the binary instead. When the
// platform grows an embedded descriptor reader, stage-before-compile and restore
// belong back in BuildMachine.
package pack
