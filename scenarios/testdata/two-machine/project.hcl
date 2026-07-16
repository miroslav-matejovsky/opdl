// Two machines in one site, which is the smallest topology that has to form a
// real fabric. The loopback addresses are distinct and every process endpoint
// is explicit, so both machines can bind on one host without fabric overrides.
project "two-machine" {
  environment = "development"

  features {
    chaos = false
  }

  site "local" {
    machine "node-a" {
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

    machine "node-b" {
      role     = "all-in-one"
      ip       = "127.0.0.2"
      services = ["core-services"]
      platform {
        instance "primary" {
          api_address = "127.0.0.2:8080"
          fabric_client_address = "127.0.0.2:3320"
          fabric_memberlist_address = "127.0.0.2:3322"
        }
        instance "secondary" {
          api_address = "127.0.0.2:8081"
          fabric_client_address = "127.0.0.2:3321"
          fabric_memberlist_address = "127.0.0.2:3323"
        }
      }
    }
  }
}
