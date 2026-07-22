lets prepare plan for bigger refactor;
goals:
- introduce a new distribution package in the platform/internal that will be acting as an abstraction layer for NATS and will provide possibility to switch to other messaging systems in the future;
- this distribution package will provide pluggable architecture for distribution layer, so that we can use for example sqlite locally for testing and NATS for production;
- start with the blueprint, because blueprint is the declarative configuration of the platform and it is crucial for understanding the platform and its components;
- each site will have defined distribution_type, so that we can switch between different distribution types for each site;
```hcl
  site "north" {
    distribution_type = "nats"
    machine "sensor" {
      profile  = "sensor-node"
      ip       = "10.0.1.10"
      services = ["sensor-services"]

      # Every platform block is mandatory. The blocks directly under platform are
      # the Primary Instance's; standby is the Standby Instance's. This machine
      # opts out of a standby, so it states that and authors nothing further.
      platform {
        runtime_dir = "C:/ProgramData/opdl/customer-a/north/sensor/primary"

        api {
          local_port = 8080
        }

        winservice {
          name = "opdl-customer-a-north-sensor-primary"
        }

        standby {
          disabled = true
        }
      }
    }

    machine "local-server" {
      profile  = "local-server"
      ip       = "10.0.1.11"
      services = ["core-services"]

      # This machine deploys both instances. They run together on one host, so
      # every port below is distinct: nothing is shared between them except the
      # ownership object, which is not a port.
      platform {
        runtime_dir = "C:/ProgramData/opdl/customer-a/north/local-server/primary"

        api {
          local_port = 8080
        }

        winservice {
          name = "opdl-customer-a-north-local-server-primary"
        }

        nats {
          client_port  = 4222
          cluster_port = 6222
        }

        standby {
          disabled    = false
          runtime_dir = "C:/ProgramData/opdl/customer-a/north/local-server/standby"

          lock {
            windows_mutex = "Global\\opdl-customer-a-north-local-server"
          }

          api {
            local_port = 8081
          }

          winservice {
            name = "opdl-customer-a-north-local-server-standby"
          }
        }
      }
    }
    nats {
      machine "sensor" {
        client_port  = 4222
        cluster_port = 6222
        data_dir    = "D:/opdl-journal/customer-a/north/sensor/primary"
      }
      machine "local-server" {
        client_port  = 4222
        cluster_port = 6222
        data_dir    = "D:/opdl-journal/customer-a/north/local-server/standby"
      }
  }
```
- ignore scenarios for now, because scenarios are black-box tests and I need to decide
- first refactor the builder module
- store plan to docs/plan in multiple files/stages, so that we can track progress and have a clear overview of the refactor;
- each stage with complexity and effort estimate
- each stage must explicitly state that scenarios are ignored and that they will be refactored in the future;
