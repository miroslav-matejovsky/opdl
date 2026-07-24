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
      # standby states whether a second platform instance is deployed. This sensor
      # opts out: it is a single-purpose node whose loss is already covered by the
      # site, so a second instance would add a process to operate without adding
      # site availability. Because it opts out, it states nothing further.
      platform {
        data_dir = "D:/opdl/customer-a/north/sensor/primary"

        api {
          local_port = 8080
        }

        winservice {
          name         = "opdl-customer-a-north-sensor-primary"
          display_name = "OPDL customer-a north sensor (Primary Instance)"
          description  = "OPDL platform Primary Instance for machine sensor."
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
      # shared between them except the ownership lease file, which is not a port.
      #
      # Copying the primary's blocks into standby and forgetting to change the
      # ports is the mistake this shape invites. The builder rejects it and names
      # both listeners.
      platform {
        data_dir = "D:/opdl/customer-a/north/local-server/primary"

        api {
          local_port = 8080
        }

        winservice {
          name         = "opdl-customer-a-north-local-server-primary"
          display_name = "OPDL customer-a north local-server (Primary Instance)"
        }

        standby {
          disabled = false
          data_dir = "D:/opdl/customer-a/north/local-server/standby"

          # lease is mandatory when standby is enabled (disabled = false). The
          # machine's two instances contend for Primary Ownership through this
          # shared machine-wide file: the owner renews it, and the Standby takes
          # over once it lapses and the Primary's health endpoint reports it can
          # no longer serve. duration must be finite and renewal_interval shorter
          # than it. failback_stabilization is reserved for failback, which is not
          # yet implemented.
          lease {
            file                   = "D:/opdl/customer-a/north/local-server/lease"
            duration               = "15s"
            renewal_interval       = "5s"
            health_check_interval  = "2s"
            failback_stabilization = "30s"
          }

          api {
            local_port = 8081
          }

          winservice {
            name         = "opdl-customer-a-north-local-server-standby"
            display_name = "OPDL customer-a north local-server (Standby Instance)"
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
        data_dir = "D:/opdl/customer-a/control-room/master/primary"

        api {
          local_port = 8080
        }

        winservice {
          name         = "opdl-customer-a-control-room-master-primary"
          display_name = "OPDL customer-a control-room master (Primary Instance)"
        }

        standby {
          disabled = false
          data_dir = "D:/opdl/customer-a/control-room/master/standby"

          lease {
            file                   = "D:/opdl/customer-a/control-room/master/lease"
            duration               = "15s"
            renewal_interval       = "5s"
            health_check_interval  = "2s"
            failback_stabilization = "30s"
          }

          api {
            local_port = 8081
          }

          winservice {
            name         = "opdl-customer-a-control-room-master-standby"
            display_name = "OPDL customer-a control-room master (Standby Instance)"
          }
        }
      }
    }

    machine "slave" {
      profile  = "slave-server"
      ip       = "10.0.2.11"
      services = ["core-services"]
      platform {
        data_dir = "D:/opdl/customer-a/control-room/slave/primary"

        api {
          local_port = 8080
        }

        winservice {
          name         = "opdl-customer-a-control-room-slave-primary"
          display_name = "OPDL customer-a control-room slave (Primary Instance)"
        }

        standby {
          disabled = false
          data_dir = "D:/opdl/customer-a/control-room/slave/standby"

          lease {
            file                   = "D:/opdl/customer-a/control-room/slave/lease"
            duration               = "15s"
            renewal_interval       = "5s"
            health_check_interval  = "2s"
            failback_stabilization = "30s"
          }

          api {
            local_port = 8081
          }

          winservice {
            name         = "opdl-customer-a-control-room-slave-standby"
            display_name = "OPDL customer-a control-room slave (Standby Instance)"
          }
        }
      }
    }

    machine "integration" {
      profile  = "integration-server"
      ip       = "10.0.2.12"
      services = ["integration-services"]
      platform {
        data_dir = "D:/opdl/customer-a/control-room/integration/primary"

        api {
          local_port = 8080
        }

        winservice {
          name         = "opdl-customer-a-control-room-integration-primary"
          display_name = "OPDL customer-a control-room integration (Primary Instance)"
        }

        standby {
          disabled = false
          data_dir = "D:/opdl/customer-a/control-room/integration/standby"

          lease {
            file                   = "D:/opdl/customer-a/control-room/integration/lease"
            duration               = "15s"
            renewal_interval       = "5s"
            health_check_interval  = "2s"
            failback_stabilization = "30s"
          }

          api {
            local_port = 8081
          }

          winservice {
            name         = "opdl-customer-a-control-room-integration-standby"
            display_name = "OPDL customer-a control-room integration (Standby Instance)"
          }
        }
      }
    }
  }
}
