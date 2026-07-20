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

      # This fixture covers the single-slot policy (`standby` block omitted).
      platform {
        nats {
          client_address  = "127.0.0.1:4222"
          cluster_address = "127.0.0.1:6222"
          monitor_address = "127.0.0.1:8222"
        }
      }
    }
  }
}
