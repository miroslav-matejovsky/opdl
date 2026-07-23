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
      # profile is the machine's purpose. It is not called role: a machine's two
      # platform instances have roles, Primary and Standby, and those are the
      # roles operations works in.
      profile  = "sensor-node"
      ip       = "10.0.1.10"
      services = ["sensor-services"]

      # Every platform block here is mandatory on every machine.
      #
      # The blocks directly under platform state the Primary Instance's policy;
      # standby states the Standby Instance's. A machine always deploys a Primary
      # Instance, which is why its blocks need no wrapper, and the Standby
      # Instance is the optional one.
      #
      # runtime_dir is where this instance writes its status file. Each instance
      # has its own: two runtimes writing into one directory would overwrite each
      # other's evidence, and nothing would report it.
      #
      # data_dir is the general platform data root for this instance.
      #
      # api is the port this instance serves its local API on. It is called
      # local_port because the builder joins it with 127.0.0.1 and never with the
      # machine's ip: the platform API is machine-local and is not exposed to the
      # network. Each instance has its own and binds it for its whole lifetime,
      # not only while Active, so an operator can query a Standby Instance about
      # itself.
      #
      # winservice names the Windows Service that runs the instance. The platform
      # installs and manages no services and has no Service Control Manager
      # integration. These names are carried into the deployment manifest for
      # whoever installs the services, so the two fixed instance roles are
      # recognizable and named the same way on every machine.
      #
      # event_storage states the event fabric topology and storage settings.
      #
      # standby states whether a second platform instance is deployed. This sensor
      # opts out: it is a single-purpose node whose loss is already covered by the
      # site, so a second instance would add a process to operate without adding
      # site availability. Because it opts out, it states nothing further.
      platform {
        runtime_dir = "C:/ProgramData/opdl/customer-a/north/sensor/primary/runtime"
        data_dir    = "D:/opdl/customer-a/north/sensor/primary"

        api {
          local_port = 8080
        }

        winservice {
          name         = "opdl-customer-a-north-sensor-primary"
          display_name = "OPDL customer-a north sensor (Primary Instance)"
          description  = "OPDL platform Primary Instance for machine sensor."
        }

        event_storage {
          nats {
            client_port         = 4222
            cluster_port        = 6222
            jetstream_store_dir = "D:/opdl/customer-a/north/sensor/primary/eventfabric/nats"
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

      # This machine deploys both instances. They are independent runtimes that
      # run together on one host, so every port below is distinct: nothing is
      # shared between them except the ownership lock, which is not a port.
      #
      # Copying the primary's blocks into standby and forgetting to change the
      # ports is the mistake this shape invites. The builder rejects it and names
      # both listeners.
      platform {
        runtime_dir = "C:/ProgramData/opdl/customer-a/north/local-server/primary/runtime"
        data_dir    = "D:/opdl/customer-a/north/local-server/primary"

        api {
          local_port = 8080
        }

        winservice {
          name         = "opdl-customer-a-north-local-server-primary"
          display_name = "OPDL customer-a north local-server (Primary Instance)"
        }

        event_storage {
          nats {
            client_port         = 4222
            cluster_port        = 6222
            jetstream_store_dir = "D:/opdl/customer-a/north/local-server/primary/eventfabric/nats"
          }
        }

        standby {
          disabled    = false
          runtime_dir = "C:/ProgramData/opdl/customer-a/north/local-server/standby/runtime"
          data_dir    = "D:/opdl/customer-a/north/local-server/standby"

          # lock is mandatory when standby is enabled (disabled = false).
          # The machine's two instances contend for this Windows named mutex
          # in the machine-wide kernel namespace to coordinate Primary Ownership.
          lock {
            windows_mutex = "Global\\opdl-customer-a-north-local-server"
          }

          api {
            local_port = 8081
          }

          winservice {
            name         = "opdl-customer-a-north-local-server-standby"
            display_name = "OPDL customer-a north local-server (Standby Instance)"
          }

          event_storage {
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
          name         = "opdl-customer-a-control-room-master-primary"
          display_name = "OPDL customer-a control-room master (Primary Instance)"
        }

        event_storage {
          nats {
            client_port         = 4222
            cluster_port        = 6222
            jetstream_store_dir = "D:/opdl/customer-a/control-room/master/primary/eventfabric/nats"
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
            name         = "opdl-customer-a-control-room-master-standby"
            display_name = "OPDL customer-a control-room master (Standby Instance)"
          }

          event_storage {
            nats {
              client_port         = 4322
              cluster_port        = 6322
              jetstream_store_dir = "D:/opdl/customer-a/control-room/master/standby/eventfabric/nats"
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
          name         = "opdl-customer-a-control-room-slave-primary"
          display_name = "OPDL customer-a control-room slave (Primary Instance)"
        }

        event_storage {
          nats {
            client_port         = 4222
            cluster_port        = 6222
            jetstream_store_dir = "D:/opdl/customer-a/control-room/slave/primary/eventfabric/nats"
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
            name         = "opdl-customer-a-control-room-slave-standby"
            display_name = "OPDL customer-a control-room slave (Standby Instance)"
          }

          event_storage {
            nats {
              client_port         = 4322
              cluster_port        = 6322
              jetstream_store_dir = "D:/opdl/customer-a/control-room/slave/standby/eventfabric/nats"
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
          name         = "opdl-customer-a-control-room-integration-primary"
          display_name = "OPDL customer-a control-room integration (Primary Instance)"
        }

        event_storage {
          nats {
            client_port         = 4222
            cluster_port        = 6222
            jetstream_store_dir = "D:/opdl/customer-a/control-room/integration/primary/eventfabric/nats"
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
            name         = "opdl-customer-a-control-room-integration-standby"
            display_name = "OPDL customer-a control-room integration (Standby Instance)"
          }

          event_storage {
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
