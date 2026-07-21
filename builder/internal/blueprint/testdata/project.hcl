project "customer-a" {
  environment = "production"

  # global features that apply to all sites and machines in this project
  # mappable to the actual contracts with the customer and what they are paying for
  # or with chaos feature, something we can enable to test the resilience of the system on our test environments,
  # or to test the resilience of the system in production in a controlled way, with the customer aware of the test and its potential impact on their operations
  features {
    chaos = true
  }

  site "north" {
    machine "sensor" {
      profile  = "sensor-node"
      ip       = "10.0.1.10"
      services = ["sensor-services"]

      # Every platform block is mandatory. The blocks directly under platform are
      # the Primary Instance's; standby is the Standby Instance's. This machine
      # opts out of a standby, so it states that and authors nothing further.
      platform {
        api {
          port = 8080
        }

        winservice {
          name = "opdl-customer-a-north-sensor-primary"
        }

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
      profile  = "local-server"
      ip       = "10.0.1.11"
      services = ["core-services"]

      # This machine deploys both instances. They run together on one host, so
      # every port below is distinct: nothing is shared between them except the
      # ownership object, which is not a port.
      platform {
        api {
          port = 8080
        }

        winservice {
          name = "opdl-customer-a-north-local-server-primary"
        }

        nats {
          client_port  = 4222
          cluster_port = 6222
        }

        standby {
          disabled = false

          lock {
            windows_mutex = "Global\\opdl-customer-a-north-local-server"
          }

          api {
            port = 8081
          }

          winservice {
            name = "opdl-customer-a-north-local-server-standby"
          }

          nats {
            client_port  = 4322
            cluster_port = 6322
          }
        }
      }
    }
  }

  site "control-room" {
    machine "master" {
      profile  = "master-server"
      ip       = "10.0.2.10"
      services = ["core-services"]
      platform {
        api {
          port = 8080
        }

        winservice {
          name = "opdl-customer-a-control-room-master-primary"
        }

        nats {
          client_port  = 4222
          cluster_port = 6222
        }

        standby {
          disabled = false

          lock {
            windows_mutex = "Global\\opdl-customer-a-control-room-master"
          }

          api {
            port = 8081
          }

          winservice {
            name = "opdl-customer-a-control-room-master-standby"
          }

          nats {
            client_port  = 4322
            cluster_port = 6322
          }
        }
      }
    }

    machine "slave" {
      profile  = "slave-server"
      ip       = "10.0.2.11"
      services = ["core-services"]
      platform {
        api {
          port = 8080
        }

        winservice {
          name = "opdl-customer-a-control-room-slave-primary"
        }

        nats {
          client_port  = 4222
          cluster_port = 6222
        }

        standby {
          disabled = false

          lock {
            windows_mutex = "Global\\opdl-customer-a-control-room-slave"
          }

          api {
            port = 8081
          }

          winservice {
            name = "opdl-customer-a-control-room-slave-standby"
          }

          nats {
            client_port  = 4322
            cluster_port = 6322
          }
        }
      }
    }

    machine "integration" {
      profile  = "integration-server"
      ip       = "10.0.2.12"
      services = ["integration-services"]
      platform {
        api {
          port = 8080
        }

        winservice {
          name = "opdl-customer-a-control-room-integration-primary"
        }

        nats {
          client_port  = 4222
          cluster_port = 6222
        }

        standby {
          disabled = false

          lock {
            windows_mutex = "Global\\opdl-customer-a-control-room-integration"
          }

          api {
            port = 8081
          }

          winservice {
            name = "opdl-customer-a-control-room-integration-standby"
          }

          nats {
            client_port  = 4322
            cluster_port = 6322
          }
        }
      }
    }
  }
}
