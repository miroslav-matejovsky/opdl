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
// instance's: the lease file is the one thing the two instances share, and it is
// what makes exactly one of them Active.
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
// Windows-only and two spellings of one path are one file. The lease file is
// deliberately not in that comparison: it is the machine's, and the two
// instances sharing it is the point.
package deployment
