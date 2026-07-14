// Package topology is the topology contract: the shapes that describe what a
// distribution should contain, independent of how any machine is executed.
//
// A Project names an environment and holds Sites; a Site holds Machines; a
// Machine names a role, a network address, and the service groups assigned
// to it. Together these describe the components of a distribution and how
// they are grouped.
//
// Features are the project-level capability switches: chaos enables
// deliberately injecting failures to test the system's resilience, and
// redundancy runs two instances of platform services in parallel on the same
// machine so one can fail without taking the service down.
//
// # Validation
//
// Validate enforces the model's structural rules: identity is present, site
// and machine names are unique within the project, every machine has a role
// and a valid IP address, and every machine lists at least one service with
// no duplicates. Validation fails fast on the first violation.
//
// The hcl struct tags are the authoring wire format. They are metadata only:
// this package decodes nothing itself and depends on no HCL library, so the
// topology model is usable wherever these Go types are. Package tests verify
// authored HCL data fragments against these structs to make wire ownership
// clear and ensure HCL struct tags stay accurate.
package topology
