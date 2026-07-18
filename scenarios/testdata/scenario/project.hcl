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

      # This fixture covers the explicit single-process policy.
      platform {
        warm_standby = false
      }
    }
  }
}
