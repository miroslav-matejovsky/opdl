// Package deployment defines the deployment descriptor: one machine's complete
// deployment definition, projected from the project blueprint by the builder.
//
// The Descriptor is the builder's output contract. It carries a machine's place
// in the topology (project, environment, site, machine, role, ip), the services
// it hosts, the project features enabled on it, the machine's warm-standby
// instance policy, and the product-line Platform identity the builder stamps on.
// Its fabric section contains the other members of the machine's site; the
// machine's own identity is already at descriptor level. The platform runtime is
// what conforms to this contract at deploy time.
//
// Validate checks a descriptor is complete enough to deploy. The builder's
// resolve stage calls it before compiling, so a machine that would not boot is
// rejected before any binary is produced.
package deployment
