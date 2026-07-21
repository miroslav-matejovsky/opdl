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
      # api is the port this instance serves its local API on. Each instance has
      # its own and binds it for its whole lifetime, not only while Active, so an
      # operator can query a Standby Instance about itself.
      #
      # winservice names the Windows Service that runs the instance. The platform
      # installs and manages no services and has no Service Control Manager
      # integration. These names are carried into the deployment manifest for
      # whoever installs the services, so the two fixed instance roles are
      # recognizable and named the same way on every machine.
      #
      # nats states the ports this instance's Event Fabric server needs open. The
      # builder joins them with the machine's ip and derives the site's route and
      # server lists from the site topology.
      #
      # standby states whether a second platform instance is deployed. This sensor
      # opts out: it is a single-purpose node whose loss is already covered by the
      # site, so a second instance would add a process to operate without adding
      # site availability. Because it opts out, it states nothing further.
      platform {
        api {
          port = 8080
        }

        winservice {
          name         = "opdl-customer-a-north-sensor-primary"
          display_name = "OPDL customer-a north sensor (Primary Instance)"
          description  = "OPDL platform Primary Instance for machine sensor."
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

      # This machine deploys both instances. They are independent runtimes that
      # run together on one host, so every port below is distinct: nothing is
      # shared between them except the ownership object, which is not a port.
      #
      # Copying the primary's blocks into standby and forgetting to change the
      # ports is the mistake this shape invites. The builder rejects it and names
      # both listeners.
      platform {
        api {
          port = 8080
        }

        winservice {
          name         = "opdl-customer-a-north-local-server-primary"
          display_name = "OPDL customer-a north local-server (Primary Instance)"
        }

        nats {
          client_port  = 4222
          cluster_port = 6222
        }

        standby {
          disabled = false

          api {
            port = 8081
          }

          winservice {
            name         = "opdl-customer-a-north-local-server-standby"
            display_name = "OPDL customer-a north local-server (Standby Instance)"
          }

          nats {
            client_port  = 4322
            cluster_port = 6322
          }
        }

        # fence is optional and almost always omitted. The machine's two instances
        # contend for one Windows named mutex, and this is the only part of its
        # name a blueprint states: the builder derives the rest from the machine's
        # full identity, so two machines can never be given the same object.
        #
        # Author it only when two deployments of the same project, environment,
        # site, and machine must run on one host without sharing ownership, such as
        # a test rig running two copies side by side. Omitting it uses "opdl".
        fence {
          namespace = "opdl"
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
          name         = "opdl-customer-a-control-room-master-primary"
          display_name = "OPDL customer-a control-room master (Primary Instance)"
        }

        nats {
          client_port  = 4222
          cluster_port = 6222
        }

        standby {
          disabled = false

          api {
            port = 8081
          }

          winservice {
            name         = "opdl-customer-a-control-room-master-standby"
            display_name = "OPDL customer-a control-room master (Standby Instance)"
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
          name         = "opdl-customer-a-control-room-slave-primary"
          display_name = "OPDL customer-a control-room slave (Primary Instance)"
        }

        nats {
          client_port  = 4222
          cluster_port = 6222
        }

        standby {
          disabled = false

          api {
            port = 8081
          }

          winservice {
            name         = "opdl-customer-a-control-room-slave-standby"
            display_name = "OPDL customer-a control-room slave (Standby Instance)"
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
          name         = "opdl-customer-a-control-room-integration-primary"
          display_name = "OPDL customer-a control-room integration (Primary Instance)"
        }

        nats {
          client_port  = 4222
          cluster_port = 6222
        }

        standby {
          disabled = false

          api {
            port = 8081
          }

          winservice {
            name         = "opdl-customer-a-control-room-integration-standby"
            display_name = "OPDL customer-a control-room integration (Standby Instance)"
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
