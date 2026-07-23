# Minimal blueprint fixture for the build package's integration test.
#
# It is the smallest site the builder accepts: two machines and three platform
# instances (node-b deploys a standby). That keeps the build to two machine
# binaries while still exercising both a standby-less machine and one with a
# standby. It is deliberately self-contained here rather than reusing
# examples/, so the test does not break when an example changes.
project "buildtest" {
  environment = "test"

  features {
    chaos = false
  }

  site "solo" {
    machine "node-a" {
      profile  = "test-node"
      ip       = "10.0.0.10"
      services = ["test-services"]

      platform {
        data_dir    = "D:/opdl/buildtest/solo/node-a/primary"

        api {
          local_port = 8080
        }

        winservice {
          name         = "opdl-buildtest-solo-node-a-primary"
          display_name = "OPDL buildtest solo node-a (Primary Instance)"
        }

        event_storage {
          nats {
            client_port         = 4222
            cluster_port        = 6222
            jetstream_store_dir = "D:/opdl/buildtest/solo/node-a/primary/eventfabric/nats"
          }
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
        data_dir    = "D:/opdl/buildtest/solo/node-b/primary"

        api {
          local_port = 8080
        }

        winservice {
          name         = "opdl-buildtest-solo-node-b-primary"
          display_name = "OPDL buildtest solo node-b (Primary Instance)"
        }

        event_storage {
          nats {
            client_port         = 4222
            cluster_port        = 6222
            jetstream_store_dir = "D:/opdl/buildtest/solo/node-b/primary/eventfabric/nats"
          }
        }

        standby {
          disabled    = false
          data_dir    = "D:/opdl/buildtest/solo/node-b/standby"

          lock {
            windows_mutex = "Global\\opdl-buildtest-solo-node-b"
          }

          api {
            local_port = 8081
          }

          winservice {
            name         = "opdl-buildtest-solo-node-b-standby"
            display_name = "OPDL buildtest solo node-b (Standby Instance)"
          }

          event_storage {
            nats {
              client_port         = 4322
              cluster_port        = 6322
              jetstream_store_dir = "D:/opdl/buildtest/solo/node-b/standby/eventfabric/nats"
            }
          }
        }
      }
    }
  }
}
