// Package resolve translates a validated topology into a build plan: one
// deployment descriptor per machine.
//
// This is where the layered topology collapses. Values common to the project
// (identity, environment, features, database) and the machine's own role and
// service assignments are folded into a single, concrete deployment descriptor.
// The product-line identity and the standard runtime parameter policy are
// stamped on here, including local HTTP settings, optional cluster membership
// settings, and optional distributed-data settings. Each descriptor is
// validated, so the builder fails before it ever runs a compile on a machine
// that would not boot. Static redundancy policy is copied to both descriptors
// and authority roster members, so runtime coordination receives no topology.
//
// This stage is pure: it reads topology and produces descriptors, touching no
// files. That makes it the stage the plan command prints and the easiest to
// test.
package resolve
