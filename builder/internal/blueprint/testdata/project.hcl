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
        runtime_dir = "C:/ProgramData/opdl/customer-a/north/sensor/primary/runtime"
        data_dir    = "D:/opdl/customer-a/north/sensor/primary"

        api {
          local_port = 8080
        }

        winservice {
          name = "opdl-customer-a-north-sensor-primary"
        }

        event_storage {
          eventfabric {
            nats {
              client_port         = 4222
              cluster_port        = 6222
              jetstream_store_dir = "D:/opdl/customer-a/north/sensor/primary/eventfabric/nats"
            }
          }
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
        runtime_dir = "C:/ProgramData/opdl/customer-a/north/local-server/primary/runtime"
        data_dir    = "D:/opdl/customer-a/north/local-server/primary"

        api {
          local_port = 8080
        }

        winservice {
          name = "opdl-customer-a-north-local-server-primary"
        }

        event_storage {
          eventfabric {
            nats {
              client_port         = 4222
              cluster_port        = 6222
              jetstream_store_dir = "D:/opdl/customer-a/north/local-server/primary/eventfabric/nats"
            }
          }
        }

        standby {
          disabled    = false
          runtime_dir = "C:/ProgramData/opdl/customer-a/north/local-server/standby/runtime"
          data_dir    = "D:/opdl/customer-a/north/local-server/standby"

          lock {
            windows_mutex = "Global\\opdl-customer-a-north-local-server"
          }

          api {
            local_port = 8081
          }

          winservice {
            name = "opdl-customer-a-north-local-server-standby"
          }

          event_storage {
            eventfabric {
              nats {
                client_port         = 4322
                cluster_port        = 6322
                jetstream_store_dir = "D:/opdl/customer-a/north/local-server/standby/eventfabric/nats"
              }
            }
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
        runtime_dir = "C:/ProgramData/opdl/customer-a/control-room/master/primary/runtime"
        data_dir    = "D:/opdl/customer-a/control-room/master/primary"

        api {
          local_port = 8080
        }

        winservice {
          name = "opdl-customer-a-control-room-master-primary"
        }

        event_storage {
          eventfabric {
            nats {
              client_port         = 4222
              cluster_port        = 6222
              jetstream_store_dir = "D:/opdl/customer-a/control-room/master/primary/eventfabric/nats"
            }
          }
        }

        standby {
          disabled    = false
          runtime_dir = "C:/ProgramData/opdl/customer-a/control-room/master/standby/runtime"
          data_dir    = "D:/opdl/customer-a/control-room/master/standby"

          lock {
            windows_mutex = "Global\\opdl-customer-a-control-room-master"
          }

          api {
            local_port = 8081
          }

          winservice {
            name = "opdl-customer-a-control-room-master-standby"
          }

          event_storage {
            eventfabric {
              nats {
                client_port         = 4322
                cluster_port        = 6322
                jetstream_store_dir = "D:/opdl/customer-a/control-room/master/standby/eventfabric/nats"
              }
            }
          }
        }
      }
    }

    machine "slave" {
      profile  = "slave-server"
      ip       = "10.0.2.11"
      services = ["core-services"]
      platform {
        runtime_dir = "C:/ProgramData/opdl/customer-a/control-room/slave/primary/runtime"
        data_dir    = "D:/opdl/customer-a/control-room/slave/primary"

        api {
          local_port = 8080
        }

        winservice {
          name = "opdl-customer-a-control-room-slave-primary"
        }

        event_storage {
          eventfabric {
            nats {
              client_port         = 4222
              cluster_port        = 6222
              jetstream_store_dir = "D:/opdl/customer-a/control-room/slave/primary/eventfabric/nats"
            }
          }
        }

        standby {
          disabled    = false
          runtime_dir = "C:/ProgramData/opdl/customer-a/control-room/slave/standby/runtime"
          data_dir    = "D:/opdl/customer-a/control-room/slave/standby"

          lock {
            windows_mutex = "Global\\opdl-customer-a-control-room-slave"
          }

          api {
            local_port = 8081
          }

          winservice {
            name = "opdl-customer-a-control-room-slave-standby"
          }

          event_storage {
            eventfabric {
              nats {
                client_port         = 4322
                cluster_port        = 6322
                jetstream_store_dir = "D:/opdl/customer-a/control-room/slave/standby/eventfabric/nats"
              }
            }
          }
        }
      }
    }

    machine "integration" {
      profile  = "integration-server"
      ip       = "10.0.2.12"
      services = ["integration-services"]
      platform {
        runtime_dir = "C:/ProgramData/opdl/customer-a/control-room/integration/primary/runtime"
        data_dir    = "D:/opdl/customer-a/control-room/integration/primary"

        api {
          local_port = 8080
        }

        winservice {
          name = "opdl-customer-a-control-room-integration-primary"
        }

        event_storage {
          eventfabric {
            nats {
              client_port         = 4222
              cluster_port        = 6222
              jetstream_store_dir = "D:/opdl/customer-a/control-room/integration/primary/eventfabric/nats"
            }
          }
        }

        standby {
          disabled    = false
          runtime_dir = "C:/ProgramData/opdl/customer-a/control-room/integration/standby/runtime"
          data_dir    = "D:/opdl/customer-a/control-room/integration/standby"

          lock {
            windows_mutex = "Global\\opdl-customer-a-control-room-integration"
          }

          api {
            local_port = 8081
          }

          winservice {
            name = "opdl-customer-a-control-room-integration-standby"
          }

          event_storage {
            eventfabric {
              nats {
                client_port         = 4322
                cluster_port        = 6322
                jetstream_store_dir = "D:/opdl/customer-a/control-room/integration/standby/eventfabric/nats"
              }
            }
          }
        }
      }
    }
  }
}
