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

      # warm standby is enabled by default; this machine opts out and runs a
      # single slot.
      platform {
        warm_standby = false
      }
    }

    machine "local-server" {
      role     = "local-server"
      ip       = "10.0.1.11"
      services = ["core-services"]
    }
  }

  site "control-room" {
    machine "master" {
      role     = "master-server"
      ip       = "10.0.2.10"
      services = ["core-services"]
    }
    machine "slave" {
      role     = "slave-server"
      ip       = "10.0.2.11"
      services = ["core-services"]
    }
    machine "integration" {
      role     = "integration-server"
      ip       = "10.0.2.12"
      services = ["integration-services"]
    }
  }
}
