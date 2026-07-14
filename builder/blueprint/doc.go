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
// Package topology is the topology contract: the shapes that describe what a
// distribution should contain, independent of how any machine is executed.
//
// A Project names an environment and holds Sites; a Site holds Machines; a
// Machine names a role, a network address, and the service groups assigned
// to it. Together these describe the components of a distribution and how
// they are grouped.
//
// Features are the project-level capability switches: chaos enables
// deliberately injecting failures to test the system's resilience, and
// redundancy runs two instances of platform services in parallel on the same
// machine so one can fail without taking the service down.
//
// # Validation
//
// Validate enforces the model's structural rules: identity is present, site
// and machine names are unique within the project, every machine has a role
// and a valid IP address, and every machine lists at least one service with
// no duplicates. Validation fails fast on the first violation.
//
// The hcl struct tags are the authoring wire format. They are metadata only:
// this package decodes nothing itself and depends on no HCL library, so the
// topology model is usable wherever these Go types are. Package tests verify
// authored HCL data fragments against these structs to make wire ownership
// clear and ensure HCL struct tags stay accurate.
package blueprint
