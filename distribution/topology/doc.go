// Package topology is the topology contract: the shapes that describe what a
// distribution should contain, independent of how any machine is executed.
//
// Topology is intent and structure. A Project names an environment and a
// backing store and holds Sites; a Site holds Machines; a Machine names a role
// and the catalog service kinds assigned to it, with optional per-assignment
// parameters. Together those describe the components of a distribution and how
// they are grouped, not how a single machine's binary boots.
//
// # Authored, not executed
//
// Topology is the authoring side of the line. The blueprint authors and
// validates it (from HCL); the builder reads it and translates it into
// per-machine deployment descriptors. The platform runtime never sees topology:
// it consumes only the deployment a machine is resolved into. Keeping topology
// and deployment as separate contracts lets each evolve on its own, and keeps
// whole-distribution structure out of a single machine's runtime.
//
// # Validation
//
// Validate enforces the model's rules against the shared catalog: identity is
// present, site and machine names are unique, a machine runs only known catalog
// service kinds, and a service is assigned only where its required feature is
// enabled on the project. It also validates static single-active service groups:
// every listed site member must host the service and repeat the same policy.
// Validation fails fast on the first violation so a
// broken topology never reaches the builder.
//
// The hcl struct tags are the authoring wire format. They are metadata only:
// this package decodes nothing itself and depends on no HCL library, so the
// topology model is usable wherever these Go types are. Package tests verify
// authored HCL data fragments against these structs to make wire ownership
// clear and ensure HCL struct tags stay accurate.
package topology
