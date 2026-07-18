// Package resolve translates a validated blueprint into a build plan: one
// deployment descriptor per machine.
//
// This is where the layered topology collapses. Project-wide values (identity,
// environment, features) and each machine's own role, address, services, and
// warm-standby policy are folded into a single, concrete deployment descriptor,
// with the product-line identity stamped on by the builder. A machine that omits
// a warm-standby policy resolves to the default of enabled, so the descriptor
// always states the result. Each descriptor is validated, so the builder fails
// before it ever compiles a machine that would not boot.
//
// This stage is pure: it reads a blueprint and produces descriptors, touching
// no files. That makes it the stage the plan command prints and the easiest to
// test.
package resolve
