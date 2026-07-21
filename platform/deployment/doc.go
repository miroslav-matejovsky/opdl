// Package deployment is the platform's view of the deployment descriptor: one
// machine's deployment definition (identity, hosted services, enabled features,
// and peer fabric topology).
//
// It is the platform side of the build output contract the builder produces. The
// platform keeps its own copy of the descriptor types rather than importing the
// builder, so the runtime never depends on the build tool. The conformance-tests module
// imports this package alongside the builder's descriptor to verify the two stay
// compatible; that is why it is exported rather than internal.
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
// The NATS topology is machine-level rather than per slot, because the primary
// and standby are mutually exclusive holders of Primary Ownership and therefore of
// one set of endpoints. Both process roles compose the same configuration from
// it; only client-only conversion distinguishes a process that does not hold the
// fence, and it removes ownership rather than selecting a different endpoint.
// There is no monitor address: the platform runs no NATS monitoring listener.
//
// # Why decoding is strict
//
// UnmarshalJSON requires slots, both slot records, both disabled fields, and
// event_fabric.nats to be present. Each of those guards a value with a usable
// zero: an omitted standby disabled would decode as false and deploy redundancy
// nobody asked for, and an omitted nats block would decode as a machine with no
// journal to reach. Failing at decode turns a truncated or stale descriptor into
// a startup error instead of a running machine with the wrong topology.
package deployment
