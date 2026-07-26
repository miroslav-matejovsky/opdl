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
    machine "node-a" {
      profile  = "test-node"
      ip       = "10.0.0.10"
      services = ["test-services"]

      platform {
        events_file = "D:/opdl/buildtest/solo/node-a/primary/events.jsonl"
        state_file  = "D:/opdl/buildtest/solo/node-a/primary/state.json"

        api {
          local_port = 8080
          read_header_timeout = "5s"
          shutdown_timeout    = "10s"
        }

        winservice {
          name         = "opdl-buildtest-solo-node-a-primary"
          display_name = "OPDL buildtest solo node-a (Primary Instance)"
        }

        standby {
          disabled = true
        }
      }
    }

    machine "node-b" {
      profile  = "test-node"
      ip       = "10.0.0.11"
      services = ["test-services"]

      platform {
        events_file = "D:/opdl/buildtest/solo/node-b/primary/events.jsonl"
        state_file  = "D:/opdl/buildtest/solo/node-b/primary/state.json"

        api {
          local_port = 8080
          read_header_timeout = "5s"
          shutdown_timeout    = "10s"
        }

        winservice {
          name         = "opdl-buildtest-solo-node-b-primary"
          display_name = "OPDL buildtest solo node-b (Primary Instance)"
        }

        standby {
          disabled    = false
          events_file = "D:/opdl/buildtest/solo/node-b/standby/events.jsonl"
          state_file  = "D:/opdl/buildtest/solo/node-b/standby/state.json"

          lease {
            file                   = "D:/opdl/buildtest/solo/node-b/lease"
            duration               = "15s"
            renewal_interval       = "5s"
            health_check_interval  = "2s"
            failback_stabilization = "30s"
            lag_bound              = "30s"
          }

          api {
            local_port = 8081
            read_header_timeout = "5s"
            shutdown_timeout    = "10s"
          }

          winservice {
            name         = "opdl-buildtest-solo-node-b-standby"
            display_name = "OPDL buildtest solo node-b (Standby Instance)"
          }
        }
      }
    }
  }
}
