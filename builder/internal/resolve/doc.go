// Package resolve translates a validated blueprint into a build plan: one
// deployment descriptor per machine.
//
// This is where the layered topology collapses. Project-wide values (identity,
// environment) and each machine's own role, address, services, NATS
// ports, and standby policy are folded into a single, concrete deployment
// descriptor, with the product-line identity stamped on by the builder. The
// blueprint is validated first and each descriptor after, so the builder fails
// before it ever compiles a machine that would not boot.
//
// # What is derived rather than authored
//
// A blueprint authors two NATS ports per machine. Everything else about the
// Event Fabric's topology is a consequence of the site and is derived here:
//
//   - each machine's client and cluster addresses, by joining its authored ports
//     with its ip;
//   - which machines store the site journal, by sorted machine name: one for a
//     site smaller than three machines, the first three otherwise;
//   - the server list every machine reaches the journal through, with a storage
//     machine's own address first so it answers its own clients;
//   - the route list, which only a storage machine has and which names only the
//     site's other storage machines.
//
// None of these may be authored. A blueprint that could state a route or server
// list directly could split a site, omit a storage node, or point a machine at
// another site's journal, and the descriptor that produced would look exactly
// like a working one.
//
// There is one NATS topology per machine, not one per process slot. The primary
// and standby are mutually exclusive holders of Primary Ownership and therefore of
// the machine's endpoints, so the resolved topology lives on the descriptor's
// Event Fabric rather than on a slot. The slots carry only their explicit
// disabled decision.
//
// Empty route and server lists resolve as empty rather than nil, so a descriptor
// reader can tell "resolved to nothing" from "not resolved".
//
// This stage is pure: it reads a blueprint and produces descriptors, touching
// no files. That makes it the stage the plan command prints and the easiest to
// test.
package resolve
