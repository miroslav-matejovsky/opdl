project "customer-a" {
  environment = "production"

  # global features that apply to all sites and machines in this project
  # mappable to the actual contracts with the customer and what they are paying for
  # or with chaos feature, something we can enable to test the resilience of the system on our test environments,
  # or to test the resilience of the system in production in a controlled way, with the customer aware of the test and its potential impact on their operations
  features {
    chaos = true
  }

  site "north" {
    machine "sensor" {
      role     = "sensor-node"
      ip       = "10.0.1.10"
      services = ["sensor-services"]

      # primary slot is defined by platform.nats; standby is omitted so only primary is deployed.
      platform {
        nats {
          client_address  = "10.0.1.10:4222"
          cluster_address = "10.0.1.10:6222"
          monitor_address = "127.0.0.1:8222"
        }
      }
    }

    machine "local-server" {
      role     = "local-server"
      ip       = "10.0.1.11"
      services = ["core-services"]
      platform {
        nats {
          client_address  = "10.0.1.11:4222"
          cluster_address = "10.0.1.11:6222"
          monitor_address = "127.0.0.1:8222"
        }
      }
    }
  }

  site "control-room" {
    machine "master" {
      role     = "master-server"
      ip       = "10.0.2.10"
      services = ["core-services"]
      platform {
        nats {
          client_address  = "10.0.2.10:4222"
          cluster_address = "10.0.2.10:6222"
          monitor_address = "127.0.0.1:8222"
        }
      }
    }
    machine "slave" {
      role     = "slave-server"
      ip       = "10.0.2.11"
      services = ["core-services"]
      platform {
        nats {
          client_address  = "10.0.2.11:4222"
          cluster_address = "10.0.2.11:6222"
          monitor_address = "127.0.0.1:8222"
        }
      }
    }
    machine "integration" {
      role     = "integration-server"
      ip       = "10.0.2.12"
      services = ["integration-services"]
      platform {
        nats {
          client_address  = "10.0.2.12:4222"
          cluster_address = "10.0.2.12:6222"
          monitor_address = "127.0.0.1:8222"
        }
      }
    }
  }
}
