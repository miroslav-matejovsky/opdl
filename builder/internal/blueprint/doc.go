// Package blueprint authors, decodes, and validates the topology of a
// distribution from HCL. It is purely a model layer: it knows nothing about
// building binaries, deployment descriptors, or the platform runtime. It
// understands topology only: what should exist, and where.
//
// # The model
//
// A blueprint describes one project as a nested hierarchy of sites and
// machines:
//
//	project "customer-a" {
//	  environment = "production"
//
//	  features {
//	    chaos = true
//	  }
//
//	  site "north" {
//	    machine "sensor" {
//	      role     = "sensor-node"
//	      ip       = "10.0.1.10"
//	      services = ["sensor-services"]
//	      platform {
//	        instance "primary" {
//	          api_address = "10.0.1.10:8080"
//	          fabric_client_address = "10.0.1.10:3320"
//	          fabric_memberlist_address = "10.0.1.10:3322"
//	        }
//	        instance "secondary" {
//	          api_address = "10.0.1.10:8081"
//	          fabric_client_address = "10.0.1.10:3321"
//	          fabric_memberlist_address = "10.0.1.10:3323"
//	        }
//	      }
//	    }
//	  }
//	}
//
// A Project names an environment and holds Features and Sites; a Site holds
// Machines; a Machine names a role, an IP address, its assigned services, and
// the explicit endpoints of its primary and optional secondary platform
// processes. Secondary is enabled unless explicitly disabled. Features are
// project-level capability switches; chaos enables controlled failure injection.
// Endpoint ports are authored facts. Production code supplies no defaults.
//
// The hcl struct tags on these types are the authoring wire format and the
// only contract this package exposes; there is no separate model to keep in
// sync.
//
// # Loading and validation
//
// Load reads every *.hcl file in a blueprint directory, merges them, and
// decodes the single project block they must describe. It then calls
// Validate, which enforces the model's structural rules: identity is
// present, site and machine names are unique within the project, every
// machine has a role and a valid IP address, and every machine lists at
// least one service with no duplicates, and every enabled platform process has
// complete, collision-free endpoints. Validation fails fast on the first
// violation, so every downstream consumer can assume a well-formed Project.
// Translating a Project into deployment descriptors is the builder's job,
// not this package's.
package blueprint
