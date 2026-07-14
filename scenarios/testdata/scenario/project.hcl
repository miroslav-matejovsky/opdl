project "scenario" {
  environment = "development"

  features {
    chaos      = false
    redundancy = false
  }

  site "local" {
    machine "node" {
      role     = "all-in-one"
      ip       = "127.0.0.1"
      services = ["core-services"]
    }
  }
}
