// Package blueprint authors and validates topology from HCL. It is purely a
// model layer and knows nothing about building binaries, deployment descriptors,
// or the platform runtime. It understands topology only: what should exist.
//
// # The model
//
// A blueprint describes one project as a nested hierarchy that mirrors the
// distribution scopes:
//
//	project "customer-a" {          // project scope: identity, environment, db, features
//	  environment = "production"
//	  database { ... }
//	  features  { opcua = true ... }
//
//	  site "north" {                // site scope: a physical/logical location
//	    machine "sensor-01" {       // machine scope: one deployable unit
//	      role     = "sensor-node"
//	      services = ["sensor-service", "opcua-adapter"]
//	      service "opcua-adapter" { endpoint = "opc.tcp://plc:4840" }  // service assignment
//	    }
//	  }
//	}
//
// The HCL decodes directly into the topology contract (distribution/topology);
// this package holds no model types of its own. Each machine becomes exactly one
// deployment descriptor downstream, in the builder.
//
// # Validation
//
// Load reads every *.hcl file in a blueprint directory into one topology Project
// and calls its Validate, which enforces the model's rules against the shared
// catalog: names are unique and present, a machine may only run catalog service
// kinds, and a service is assigned only where its required feature is enabled on
// the project. Validation happens at load time so every downstream consumer can
// assume a well-formed topology. Translating a Project into deployment
// descriptors is the builder's job, not this package's.
package blueprint
