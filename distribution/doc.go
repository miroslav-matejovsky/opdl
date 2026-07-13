// Package distribution is the root of the shared model of an OPDL
// distribution: the contracts that flow through the build-and-deploy line, from
// the blueprint (author) through the builder (derive) to the platform (run).
//
// These are the stable vocabulary the pipeline modules agree on: the shapes of
// data and configuration that cross a module boundary. It is deliberately the
// only module with no internal dependencies, so every other module may depend on
// distribution and distribution depends on nothing but the standard library.
//
// # What lives here
//
//   - catalog:       the platform's capability catalog of service kinds and
//     feature keys: the vocabulary the topology validates against and the
//     platform hosts. It is closed to projects (a project selects kinds, it
//     never invents them) but extended by the platform: a new service kind is a
//     platform capability, added here alongside its runtime implementation.
//   - topology:      the topology contract: what a distribution should contain
//     (projects, sites, machines, roles, service assignments). It is intent and
//     structure, authored by the blueprint and read by the builder. It never
//     reaches the platform runtime.
//   - deployment:    the deployment contract: how one machine is executed. The
//     builder derives a per-machine Descriptor from the topology and marshals it
//     to JSON; the platform unmarshals and runs from it. It is the deployment
//     contract between the builder (producer) and the platform (consumer), not a
//     transport wire protocol: a derived deployment artifact both sides
//     understand.
//
// Runtime message contracts (the sensor object stream and alarm transitions) are
// deliberately not here: they are consumed only by the platform runtime, so they
// live inside it (platform/internal/contracts) rather than on the cross-module
// boundary. Keeping this module to the build-and-deploy contracts is what makes
// its one-directional, boundary-only role structural.
//
// # Language neutrality
//
// The Go types here are a projection of a language-neutral model: every field
// that crosses a boundary carries an explicit wire tag (hcl for authored
// topology, json for deployment descriptors) and only uses portable scalar,
// list, and nested-record shapes. Nothing here depends on Go-specific behavior,
// so the same contracts can be expressed for another runtime without changing
// their meaning.
package distribution
