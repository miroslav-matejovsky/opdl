# Minimal blueprint fixture for the build package's integration test.
#
# It is the smallest site the builder accepts: two machines and three platform
# instances (node-b deploys a standby). That keeps the build to two machine
# binaries while still exercising both a standby-less machine and one with a
# standby. It is deliberately self-contained here rather than reusing
# examples/, so the test does not break when an example changes.
project "buildtest" {
  environment = "test"

  site "solo" {
    nats {
      cluster_name = "buildtest-solo"
    }

    machine "node-a" {
      profile         = "test-node"
      ip              = "10.0.0.10"
      eventstore_file = "D:/opdl/buildtest/solo/node-a/machine-events.jsonl"

      service "test-services" {
        role = "master"

        health_check {
          type     = "http"
          port     = 9101
          path     = "/health"
          interval = "10s"
          timeout  = "2s"
          retries  = 3
        }
      }

      primary {
        eventlog_file = "D:/opdl/buildtest/solo/node-a/primary/events.jsonl"
        state_file    = "D:/opdl/buildtest/solo/node-a/primary/state.json"
        log_file      = "D:/opdl/buildtest/solo/node-a/primary/platform.log"

        api {
          local_port          = 8080
          read_header_timeout = "5s"
          shutdown_timeout    = "10s"
        }

        nats {
          cluster_port = 6222
        }

        winservice {
          name         = "opdl-buildtest-solo-node-a-primary"
          display_name = "OPDL buildtest solo node-a (Primary Instance)"
        }
      }

      standby {
        disabled = true
      }
    }

    machine "node-b" {
      profile         = "test-node"
      ip              = "10.0.0.11"
      eventstore_file = "D:/opdl/buildtest/solo/node-b/machine-events.jsonl"

      service "test-services" {
        role = "slave"

        health_check {
          type     = "http"
          port     = 9101
          path     = "/health"
          interval = "10s"
          timeout  = "2s"
          retries  = 3
        }
      }

      primary {
        eventlog_file = "D:/opdl/buildtest/solo/node-b/primary/events.jsonl"
        state_file    = "D:/opdl/buildtest/solo/node-b/primary/state.json"
        log_file      = "D:/opdl/buildtest/solo/node-b/primary/platform.log"

        api {
          local_port          = 8080
          read_header_timeout = "5s"
          shutdown_timeout    = "10s"
        }

        nats {
          cluster_port = 6222
        }

        winservice {
          name         = "opdl-buildtest-solo-node-b-primary"
          display_name = "OPDL buildtest solo node-b (Primary Instance)"
        }
      }

      standby {
        disabled      = false
        eventlog_file = "D:/opdl/buildtest/solo/node-b/standby/events.jsonl"
        state_file    = "D:/opdl/buildtest/solo/node-b/standby/state.json"
        log_file      = "D:/opdl/buildtest/solo/node-b/standby/platform.log"

        lease {
          file                   = "D:/opdl/buildtest/solo/node-b/lease"
          duration               = "15s"
          renewal_interval       = "5s"
          health_check_interval  = "2s"
          failback_stabilization = "30s"
        }

        api {
          local_port          = 8081
          read_header_timeout = "5s"
          shutdown_timeout    = "10s"
        }

        nats {
          cluster_port = 6223
        }

        winservice {
          name         = "opdl-buildtest-solo-node-b-standby"
          display_name = "OPDL buildtest solo node-b (Standby Instance)"
        }
      }
    }
  }
}
