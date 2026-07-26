// Package resolve translates a validated blueprint into a build plan: one
// deployment descriptor per machine.
//
// This is where the layered topology collapses. Project-wide values (identity,
// environment) and each machine's own role, address, services, and standby policy
// are folded into a single, concrete deployment descriptor, with the product-line
// identity stamped on by the builder. The blueprint is validated first and each
// descriptor after, so the builder fails before it ever compiles a machine that
// would not boot.
//
// This stage is pure: it reads a blueprint and produces descriptors, touching
// no files. That makes it the stage the plan command prints and the easiest to
// test.
package resolve
