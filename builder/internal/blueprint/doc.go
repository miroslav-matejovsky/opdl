// Package blueprint authors, decodes, and validates the topology of a
// distribution from HCL.
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
//	      profile         = "sensor-node"
//	      ip              = "10.0.1.10"
//	      services        = ["sensor-services"]
//	      eventstore_file = "D:/opdl/customer-a/north/sensor/eventstore.jsonl"
//
//	      primary {
//	        eventlog_file = "D:/opdl/customer-a/north/sensor/primary/events.jsonl"
//	        state_file    = "D:/opdl/customer-a/north/sensor/primary/state.json"
//
//	        api {
//	          local_port          = 8080
//	          read_header_timeout = "5s"
//	          shutdown_timeout    = "10s"
//	        }
//
//	        winservice {
//	          name = "opdl-primary"
//	        }
//	      }
//
//	      standby {
//	        disabled = true
//	      }
//	    }
//	  }
//	}
//
// A Project names an environment and holds Sites; a Site holds Machines; a Machine
// names a role, an IP address, services, eventstore_file, a mandatory primary block,
// and a mandatory standby block (which can be disabled or configure the Standby Instance and lease).
//
// Load reads and merges HCL files in a directory, decoding and validating the
// project hierarchy.
package blueprint
