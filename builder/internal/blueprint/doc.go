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
//	  features {
//	    chaos      = true
//	    redundancy = true
//	  }
//
//	  site "north" {
//	    machine "sensor" {
//	      role     = "sensor-node"
//	      ip       = "10.0.1.10"
//	      services = ["sensor-services"]
//	    }
//	  }
//	}
//
// A Project names an environment and holds Features and Sites; a Site holds
// Machines; a Machine names a role, an IP address, and the services assigned
// to it. Features are project-level capability switches: chaos enables
// deliberately injecting failures to test the system's resilience, and
// redundancy runs two instances of platform services in parallel on the same
// machine so one can fail without taking the service down.
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
// least one service with no duplicates. Validation fails fast on the first
// violation, so every downstream consumer can assume a well-formed Project.
// Translating a Project into deployment descriptors is the builder's job,
// not this package's.
package blueprint
