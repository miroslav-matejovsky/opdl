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
//	        data_dir    = "D:/opdl/customer-a/north/sensor/primary"
//
//	        api {
//	          local_port = 8080
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
// Primary Ownership.
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
// 1-65535 that differ from each other. Validation fails fast on the first
// violation, so every downstream consumer can assume a well-formed Project.
// Translating a Project into deployment descriptors is the builder's job,
// not this package's.
package blueprint
