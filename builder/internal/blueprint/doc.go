// Package blueprint authors, decodes, and validates the topology of a
// distribution from HCL. It is purely a model layer: it knows nothing about
// building binaries, deployment descriptors, or the platform runtime. It
// understands topology only — what should exist, and where.
//
// # The model
//
// A blueprint describes one project as a nested hierarchy of sites and
// machines:
//
//	project "customer-a" {
//	  environment = "production"
//
//	  site "north" {
//	    machine "sensor" {
//	      profile  = "sensor-node"
//	      ip       = "10.0.1.10"
//	      services = ["sensor-services"]
//
//	      platform {
//	        events_file = "D:/opdl/customer-a/north/sensor/primary/events.jsonl"
//	        state_file  = "D:/opdl/customer-a/north/sensor/primary/state.json"
//
//	        api {
//	          local_port          = 8080
//	          read_header_timeout = "5s"
//	          shutdown_timeout    = "10s"
//	        }
//
//	        standby {
//	          disabled = true
//	        }
//	      }
//	    }
//	  }
//	}
//
// A Project names an environment and holds Sites; a Site holds
// Machines; a Machine names a role, an IP address, the services assigned to it,
// and its platform subsection.
//
// # The platform subsection
//
// The machine platform subsection carries platform-runtime policy, as opposed to
// what the machine deploys.
//
// standby states whether a second local process is deployed on the machine:
//
//	standby {
//	  disabled = true
//	}
//
// The attribute is required, so omitting it cannot silently enable or disable
// redundancy. A false value deploys a second local process that waits on the
// Primary Ownership, and it must then author its own events_file, state_file,
// api, winservice, and the machine's lease block.
//
// # An instance's files are named outright
//
// events_file and state_file are the local files an instance owns: the
// append-only record of everything it states, and the durable record it carries
// across restarts and crashes, which holds its epoch counter. Both are authored
// per instance rather than composed under a shared root, so every path a machine
// opens is visible in the blueprint. No two of them may be the same path, on
// either instance: the two instances run together on one host. See
// InstanceFiles.
//
// # The blueprint is the only place a machine is configured
//
// The platform reads no runtime configuration file. Everything a machine runs
// with is authored here and compiled into its binary, so this package's schema
// is the whole of it: each instance's api block carries read_header_timeout and
// shutdown_timeout alongside its port, because the listener they govern is that
// instance's; the machine's standby lease carries lag_bound alongside the
// failover timings, because a projection lag bound decides whether ownership may
// move at all and a machine that deploys no standby has neither.
//
// The hcl struct tags on these types are the authoring wire format and the
// only contract this package exposes; there is no separate model to keep in
// sync.
//
// # Loading and validation
//
// Load reads every *.hcl file in a blueprint directory, merges them, and
// decodes the single project block they must describe. It then calls
// Validate, which enforces the model's structural rules: identity is present,
// site and machine names are unique within the project, every machine has a role
// and a valid IP address, every machine lists at least one service with no
// duplicates, and every machine states platform blocks with ports in range
// 1-65535 that differ from each other and with every duration a positive Go
// duration string. Validation fails fast on the first
// violation, so every downstream consumer can assume a well-formed Project.
// Translating a Project into deployment descriptors is the builder's job,
// not this package's.
package blueprint
