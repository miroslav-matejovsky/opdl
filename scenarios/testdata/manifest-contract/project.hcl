project "manifest-contract" {
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
        nats {
          client_address  = "127.0.0.1:4222"
          cluster_address = "127.0.0.1:6222"
          monitor_address = "127.0.0.1:8222"
        }
        standby {
          nats {
            client_address  = "127.0.0.1:4223"
            cluster_address = "127.0.0.1:6223"
            monitor_address = "127.0.0.1:8223"
          }
        }
      }
    }
  }
}
