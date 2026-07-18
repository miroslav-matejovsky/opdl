// Two machines in one site, which is the smallest topology that has to form a
// real fabric. The loopback addresses are distinct so each machine derives its
// own fabric addresses on the fixed ports and both can bind on one host, which
// is what lets this run as a scenario without any runtime override.
project "two-machine" {
  environment = "development"

  features {
    chaos = false
  }

  site "local" {
    # This fixture isolates site coordination from local process redundancy.
    machine "node-a" {
      role     = "all-in-one"
      ip       = "127.0.0.1"
      services = ["core-services"]

      platform {
        warm_standby = false
      }
    }

    machine "node-b" {
      role     = "all-in-one"
      ip       = "127.0.0.2"
      services = ["core-services"]

      platform {
        warm_standby = false
      }
    }
  }
}
