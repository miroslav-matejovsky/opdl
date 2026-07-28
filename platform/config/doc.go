// Package config owns the platform's configuration: the deployment descriptor a
// binary is built with, and the effective runtime configuration derived from it.
//
// # One source
//
// A running instance is configured by exactly one thing: the deployment
// descriptor, resolved by the builder from the project blueprint and compiled
// into the binary from deployment.json (see Deployment). There is no
// configuration file beside the executable and no environment override; a
// binary's descriptor cannot be reassigned at runtime, and a site changes what a
// machine runs with by rebuilding it.
//
// Load parses every duration the descriptor carries and fails fast on a bad one.
// No implicit defaults are applied: a missing or non-positive duration is a
// startup failure rather than a value someone has to guess at later.
//
// # Per-machine and per-instance
//
// One machine has one descriptor and both of its instances read it, so nothing
// that answers for a single instance is held on the machine. Each instance's own
// API address, data directory, and listener timeouts are on its own record:
// Descriptor.Primary always, Descriptor.Standby only when the machine deploys
// one. The accessors on Config take a role for that reason, and Summary takes
// one because the role is the only thing that differs between the two startup
// blocks of a machine.
//
// # The descriptor is a contract with the builder
//
// Descriptor and its types mirror the builder's deployment descriptor field for
// field. The platform keeps its own copy rather than importing the builder, so
// the runtime never depends on the build tool; the conformance-tests module
// imports this package alongside the builder's descriptor to verify the two stay
// compatible. That is why this package is exported rather than internal.
//
// UnmarshalJSON decodes strictly: a deployed instance must state its data
// directory and both listener timeouts, every machine must state the shared
// event store its instances append to, and the lease must be present exactly
// when a standby is. Each guarded field is a string with a usable zero value, so
// an omission would otherwise decode into a running machine with a topology
// nobody wrote down.
package config
