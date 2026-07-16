project "scenario" {
  environment = "development"

  features {
    chaos = false
  }

  site "local" {
    machine "node" {
      role     = "all-in-one"
      ip       = "127.0.0.1"
      services = ["core-services"]
      platform {
        instance "primary" {
          api_address = "127.0.0.1:8080"
          fabric_client_address = "127.0.0.1:3320"
          fabric_memberlist_address = "127.0.0.1:3322"
        }
        instance "secondary" {
          api_address = "127.0.0.1:8081"
          fabric_client_address = "127.0.0.1:3321"
          fabric_memberlist_address = "127.0.0.1:3323"
        }
      }
    }
  }
}
