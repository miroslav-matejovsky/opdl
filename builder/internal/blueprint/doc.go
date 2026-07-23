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
//	    chaos = true
//	  }
//
//	  site "north" {
//	    machine "sensor" {
//	      profile  = "sensor-node"
//	      ip       = "10.0.1.10"
//	      services = ["sensor-services"]
//
//	      platform {
//	        data_dir    = "D:/opdl/customer-a/north/sensor/primary"
//	        runtime_dir = "C:/ProgramData/opdl/customer-a/north/sensor/primary/runtime"
//
//	        api {
//	          local_port = 8080
//	        }
//
//	        event_storage {
//	          nats {
//	            client_port         = 4222
//	            cluster_port        = 6222
//	            jetstream_store_dir = "D:/opdl/customer-a/north/sensor/primary/eventfabric/nats"
//	          }
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
// A Project names an environment and holds Features and Sites; a Site holds
// Machines; a Machine names a role, an IP address, the services assigned to it,
// and its platform subsection. Chaos is an implemented project-level capability
// switch.
//
// # The platform subsection
//
// The machine platform subsection carries platform-runtime policy, as opposed to
// what the machine deploys. Both of its blocks are mandatory on every machine.
//
// nats states the ports this machine's Event Fabric server needs. Only ports are
// authored: the builder joins each with the machine's ip, and derives the site's
// route and server lists from the site's topology. A blueprint that could state
// those lists directly could split a site or point a machine at another site's
// journal, and the resulting descriptor would look like a working one.
//
// standby states whether a second local process is deployed on the machine:
//
//	standby {
//	  disabled = true
//	}
//
// The attribute is required, so omitting it cannot silently enable or disable
// redundancy. A false value deploys a second local process that waits on the
// Primary Ownership. It does not add a second NATS endpoint: the two instances are
// mutually exclusive owners of the machine's one client port and one cluster
// port, so a transfer rebinds the same addresses rather than moving the site onto
// new ones.
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
// duplicates, and every machine states both platform blocks with ports in range
// 1-65535 that differ from each other. Validation fails fast on the first
// violation, so every downstream consumer can assume a well-formed Project.
// Translating a Project into deployment descriptors is the builder's job,
// not this package's.
package blueprint
