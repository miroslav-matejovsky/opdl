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
//	      eventstore_file = "D:/opdl/customer-a/north/sensor/eventstore.jsonl"
//
//	      service "sensor-services" {
//	        role = "master"
//
//	        health_check {
//	          type     = "http"
//	          port     = 9101
//	          path     = "/health"
//	          interval = "10s"
//	          timeout  = "2s"
//	          retries  = 3
//	        }
//	      }
//
//	      primary {
//	        eventlog_file = "D:/opdl/customer-a/north/sensor/primary/events.jsonl"
//	        state_file    = "D:/opdl/customer-a/north/sensor/primary/state.json"
//	        log_file      = "D:/opdl/customer-a/north/sensor/primary/platform.log"
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
// names a role, an IP address, eventstore_file, at least one service block, a
// mandatory primary block, and a mandatory standby block (which can be disabled or
// configure the Standby Instance and lease).
//
// Each service block names one service the machine hosts, the role this copy of
// it plays ("master" or "slave"), and the health check that says whether it is up.
// Both are required. The check states what to probe (type, port, and for http a
// path), how often (interval), how long one attempt may take (timeout, shorter
// than the interval), and how many consecutive failures mark the service down
// (retries). Health check ports are listeners on the machine like the instance API
// ports, so no two of them may be the same.
//
// Every deployed instance states the three local files it owns: eventlog_file
// for the events it states, state_file for its epoch, and log_file for its
// structured application log. No two of them may name one path.
//
// Load reads and merges HCL files in a directory, decoding and validating the
// project hierarchy.
package blueprint
