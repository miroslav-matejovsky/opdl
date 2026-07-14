// Package deployment is the platform's view of the deployment descriptor: one
// machine's deployment definition (identity, hosted services, enabled features).
//
// It is the platform side of the build output contract the builder produces. The
// platform keeps its own copy of the descriptor types rather than importing the
// builder, so the runtime never depends on the build tool. The conformance module
// imports this package alongside the builder's descriptor to verify the two stay
// compatible; that is why it is exported rather than internal.
package deployment
