// Package deployment defines the deployment descriptor: one machine's complete
// deployment definition, projected from the project blueprint by the builder.
//
// Descriptor is the builder's output contract, and the platform runtime is what
// conforms to it at deploy time. The types below are the contract; this comment
// covers only what their shape cannot say.
//
// # Two instances, one mandatory
//
//	{
//	  "machine_events_file": "...",
//	  "primary": {"events_file": "...", "state_file": "...", "api_address": "127.0.0.1:8080", ...},
//	  "standby": {"events_file": "...", "state_file": "...", "api_address": "127.0.0.1:8081", ...},
//	  "lease":   {"file": "...", "duration": "15s", ...}
//	}
//
// Primary is a value and Standby is a pointer, because a machine always deploys a
// Primary Instance and deploys a Standby Instance only if its blueprint says so.
// A machine with no standby omits both the standby record and the lease, so
// absence is the whole statement and there is no disabled record carrying
// endpoints nothing will bind.
//
// Lease is present exactly when Standby is. It is the machine's, not an
// instance's: it is what makes exactly one of the two Active.
//
// MachineEventsFile is the machine's too, and unlike the lease it is on every
// machine. It is the file both instances append machine-scoped events to, so
// the machine's account of itself outlives ownership moving between two
// instances that each keep only their own record. A machine that deploys one
// instance still has machine facts, which is why it is not on the standby.
//
// # Validation
//
// Validate checks a descriptor is complete enough to deploy, and the builder's
// resolve stage calls it before compiling, so a machine that would not boot is
// rejected before any binary is produced. Beyond field presence it checks what
// only the whole machine can answer: that the two instances do not share a
// service name, a local file, or a listener, that every API address is on
// loopback, and that the lease and the standby agree about whether redundancy is
// deployed.
//
// The file paths are compared cleaned and case-folded, because this repo is
// Windows-only and two spellings of one path are one file. The machine's two
// shared files, the lease and the machine event store, are in that comparison
// as themselves: the instances sharing them is the point, but an instance's own
// record resolved onto one of them is still two accounts in one file.
package deployment
