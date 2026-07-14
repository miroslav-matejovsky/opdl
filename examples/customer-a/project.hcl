project "customer-a" {
  environment = "production"

  # Project-wide capability switches. Chaos deliberately injects failures to
  # test resilience (freely in test, or in production in a controlled way with
  # the customer aware). Redundancy runs two instances of platform services in
  # parallel on the same machine so one can fail without taking the service down.
  features {
    chaos      = true
    redundancy = true
  }

  site "north" {
    machine "sensor" {
      role     = "sensor-node"
      ip       = "10.0.1.10"
      services = ["sensor-services"]
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
