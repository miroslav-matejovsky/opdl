// Package deployment defines the deployment descriptor: one machine's complete
// deployment definition, projected from the project blueprint by the builder.
//
// The Descriptor is the builder's output contract. It carries a machine's place
// in the topology (project, environment, site, machine, role, ip), the services
// it hosts, the project features enabled on it, its resolved slot decisions, its
// resolved Event Fabric topology, and the product-line Platform identity the
// builder stamps on. The platform runtime is what conforms to this contract at
// deploy time.
//
// # The resolved JSON contract
//
//	{
//	  "slots": {
//	    "primary": {"disabled": false},
//	    "standby": {"disabled": true}
//	  },
//	  "event_fabric": {
//	    "nats": {
//	      "client_address":  "10.0.1.10:4222",
//	      "cluster_address": "10.0.1.10:6222",
//	      "routes":  [],
//	      "servers": ["10.0.1.10:4222"]
//	    },
//	    "peers": []
//	  }
//	}
//
// Both slot records are always present and non-null, so a reader never infers a
// slot policy from an omitted field. The primary is never disabled: a machine
// with no primary process would deploy nothing that can serve.
//
// The NATS topology is machine-level, not per slot. The primary and standby are
// mutually exclusive owners of the machine fence and therefore of one set of
// endpoints: the standby connects to the address the active process is serving
// on, and promotion rebinds that same address rather than moving the site onto a
// second one. There is no monitor address; the platform runs no NATS monitoring
// listener.
//
// Routes and servers are always arrays, never null, so "resolved to nothing" is
// distinguishable from "not resolved".
//
// Validate checks a descriptor is complete enough to deploy. The builder's
// resolve stage calls it before compiling, so a machine that would not boot is
// rejected before any binary is produced. Beyond field presence it checks the
// resolved topology against the storage selection the descriptor implies: a
// storage machine lists its own client address first, a machine that stores
// nothing has no routes, and no route points back at the machine itself.
package deployment
