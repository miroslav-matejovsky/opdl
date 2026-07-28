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
//	    nats {
//	      cluster_name = "customer-a-north"
//	    }
//
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
//	        nats {
//	          cluster_port = 6222
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
// A Project names an environment and holds Sites; a Site names its event fabric
// cluster and holds Machines; a Machine names a role, an IP address,
// eventstore_file, at least one service block, a mandatory primary block, and a
// mandatory standby block (which can be disabled or configure the Standby
// Instance and lease).
//
// A site's nats block is required and names one thing: the cluster every
// embedded event fabric server at that site joins. It is authored at the site
// because the site is what the cluster spans, and no two sites of a project may
// name the same one; a server accepts a route only from a peer naming its
// cluster, so a shared name is what could let two sites' servers join.
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
// Every deployed instance also states a nats block: the cluster_port its own
// embedded event fabric server binds. It is the only port that server is
// authored with, because the platform's client reaches its own server in
// process and nothing outside the process is a client of it; what the cluster
// port is for is the servers of a site reaching each other. It is a listener on
// the machine like an instance api port or a service health check port, so no
// two of them may be the same.
//
// Unlike an api port, it is not resolved onto loopback. The builder joins it
// with the machine's ip, because a cluster whose members were all on 127.0.0.1
// could never have a second machine in it, and it resolves each instance's
// routes from the rest of the site. So the cluster port has to be reachable
// between the machines of a site, and nothing else about it is authored:
// membership follows from the blueprint's shape.
//
// Load reads and merges HCL files in a directory, decoding and validating the
// project hierarchy.
package blueprint
