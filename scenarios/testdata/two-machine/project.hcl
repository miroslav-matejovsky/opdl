// Two machines in one site, which is the smallest topology that has to form a
// real fabric. The loopback addresses are distinct so each machine derives its
// own fabric addresses on the fixed ports and both can bind on one host, which
// is what lets this run as a scenario without any runtime override.
project "two-machine" {
  environment = "development"

  features {
    chaos      = false
    redundancy = false
  }

  site "local" {
    machine "node-a" {
      role     = "all-in-one"
      ip       = "127.0.0.1"
      services = ["core-services"]
    }

    machine "node-b" {
      role     = "all-in-one"
      ip       = "127.0.0.2"
      services = ["core-services"]
    }
  }
}
