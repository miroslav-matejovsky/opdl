project "customer-a" {
  environment = "production"

  # Project-wide capability switches. Chaos deliberately injects failures to
  # test resilience (freely in test, or in production in a controlled way with
  # the customer aware).
  features {
    chaos = true
  }

  site "north" {
    machine "sensor" {
      role     = "sensor-node"
      ip       = "10.0.1.10"
      services = ["sensor-services"]
      platform {
        instance "primary" {
          api_address = "10.0.1.10:8080"
          fabric_client_address = "10.0.1.10:3320"
          fabric_memberlist_address = "10.0.1.10:3322"
        }
        instance "secondary" {
          api_address = "10.0.1.10:8081"
          fabric_client_address = "10.0.1.10:3321"
          fabric_memberlist_address = "10.0.1.10:3323"
        }
      }
    }

    machine "local-server" {
      role     = "local-server"
      ip       = "10.0.1.11"
      services = ["core-services"]
      platform {
        instance "primary" {
          api_address = "10.0.1.11:8080"
          fabric_client_address = "10.0.1.11:3320"
          fabric_memberlist_address = "10.0.1.11:3322"
        }
        instance "secondary" {
          api_address = "10.0.1.11:8081"
          fabric_client_address = "10.0.1.11:3321"
          fabric_memberlist_address = "10.0.1.11:3323"
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
        instance "primary" {
          api_address = "10.0.2.10:8080"
          fabric_client_address = "10.0.2.10:3320"
          fabric_memberlist_address = "10.0.2.10:3322"
        }
        instance "secondary" {
          api_address = "10.0.2.10:8081"
          fabric_client_address = "10.0.2.10:3321"
          fabric_memberlist_address = "10.0.2.10:3323"
        }
      }
    }

    machine "slave" {
      role     = "slave-server"
      ip       = "10.0.2.11"
      services = ["core-services"]
      platform {
        instance "primary" {
          api_address = "10.0.2.11:8080"
          fabric_client_address = "10.0.2.11:3320"
          fabric_memberlist_address = "10.0.2.11:3322"
        }
        instance "secondary" {
          api_address = "10.0.2.11:8081"
          fabric_client_address = "10.0.2.11:3321"
          fabric_memberlist_address = "10.0.2.11:3323"
        }
      }
    }

    machine "integration" {
      role     = "integration-server"
      ip       = "10.0.2.12"
      services = ["integration-services"]
      platform {
        secondary_enabled = false
        instance "primary" {
          api_address = "10.0.2.12:8080"
          fabric_client_address = "10.0.2.12:3320"
          fabric_memberlist_address = "10.0.2.12:3322"
        }
      }
    }
  }
}
