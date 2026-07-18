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

      # The harness launches one process per machine, so each scenario machine
      # runs a single slot rather than an active/standby pair.
      platform {
        warm_standby = false
      }
    }
  }
}
