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

      # Both platform blocks are mandatory on every machine.
      #
      # nats states the ports this machine's Event Fabric server needs open. The
      # builder joins them with the machine's ip; whichever local process holds
      # the machine fence binds them, so there is one client and at most one
      # cluster port per machine no matter how many processes are deployed.
      #
      # standby states whether a second local process is deployed to wait on that
      # fence. This sensor opts out: it is a single-purpose node whose loss is
      # already covered by the site, so a second local process would add a
      # process to operate without adding site availability.
      platform {
        nats {
          client_port  = 4222
          cluster_port = 6222
        }

        standby {
          disabled = true
        }
      }
    }

    machine "local-server" {
      role     = "local-server"
      ip       = "10.0.1.11"
      services = ["core-services"]
      platform {
        nats {
          client_port  = 4222
          cluster_port = 6222
        }

        standby {
          disabled = false
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
        nats {
          client_port  = 4222
          cluster_port = 6222
        }

        standby {
          disabled = false
        }
      }
    }

    machine "slave" {
      role     = "slave-server"
      ip       = "10.0.2.11"
      services = ["core-services"]
      platform {
        nats {
          client_port  = 4222
          cluster_port = 6222
        }

        standby {
          disabled = false
        }
      }
    }

    machine "integration" {
      role     = "integration-server"
      ip       = "10.0.2.12"
      services = ["integration-services"]
      platform {
        nats {
          client_port  = 4222
          cluster_port = 6222
        }

        standby {
          disabled = false
        }
      }
    }
  }
}
