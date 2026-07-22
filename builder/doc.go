// Package builder is the root of the OPDL build tool module.
//
// The builder is the translation step of the distribution line: it turns an
// authored blueprint into deployable machine packages. It never contains
// project-specific logic and never imports the platform runtime; it drives the
// platform's ordinary `go build`.
//
//	Blueprint  ->  Builder  ->  Deployment
//
// The module is a CLI (cmd) over two internal stages plus the shared
// deployment contract:
//
//	blueprint   authors, decodes, and validates a project's topology from HCL.
//	deployment  the deployment descriptor: one machine's deployment definition
//	            and the builder's output contract, which the platform conforms to.
//	resolve     a validated blueprint -> a plan of per-machine deployment
//	            descriptors, one per machine, each validated.
//	pack        a machine's descriptor -> a deployment package: compile the
//	            platform and assemble the binary with its descriptor, a manifest,
//	            release metadata, and checksums.
package builder
