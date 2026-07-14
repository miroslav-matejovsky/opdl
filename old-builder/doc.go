// Package builder is the root of the OPDL build tool module.
//
// The builder is the translation step of the distribution line: it turns an
// authored topology into deployable machine packages. It never contains
// project-specific logic and never imports the platform runtime; it drives the
// platform's ordinary `go build` with a machine's deployment descriptor staged
// in the embedded folder.
//
//	Topology  ->  Builder  ->  Deployment
//
// The module is organized as a CLI (cmd/opdl) over two internal stages:
//
//	resolve  a validated topology -> a plan of per-machine deployment
//	         descriptors, one per machine, each validated.
//	pack     a machine's descriptor -> a deployment package: stage the descriptor
//	         as JSON, run go build, assemble the binary with its descriptor, a
//	         deployment manifest, release metadata, and checksums, then restore
//	         the placeholder descriptor so the working tree is left clean.
package builder
