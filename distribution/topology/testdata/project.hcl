project "customer-a" {
  environment = "production"

  # global features that apply to all sites and machines in this project
  # mappable to the actual contracts with the customer and what they are paying for
  # or with chaos feature, something we can enable to test the resilience of the system on our test environments,
  # or to test the resilience of the system in production in a controlled way, with the customer aware of the test and its potential impact on their operations
  features {
    chaos = true
    # redundancy is enabling two instances of platform services to run in parallel on the same machine, to provide redundancy in case one instance fails.
    # This is useful for critical services that need to be highly available, but it also increases resource usage and complexity.
    redundancy = true
  }

  site "north" {
    machine "sensor" {
      role     = "sensor-node"
      services = ["sensor-services"]
    }

    machine "local-server" {
      role     = "local-server"
      services = ["core-services"]
    }
  }

  site "control-room" {
    machine "master" {
      role     = "master-server"
      services = ["core-services"]
    }
    machine "slave" {
      role     = "slave-server"
      services = ["core-services"]
    }
    machine "integration" {
      role     = "integration-server"
      services = ["integration-services"]
    }
  }
}
