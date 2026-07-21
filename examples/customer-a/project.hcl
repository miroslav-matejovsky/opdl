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

      # Every platform block here is mandatory on every machine.
      #
      # winservice names the Windows Service that runs the Primary Instance. A
      # machine always deploys a Primary Instance, so this block is always
      # present; the Standby Instance's service is named inside standby, because
      # that instance is the optional one.
      #
      # The platform installs and manages no services and has no Service Control
      # Manager integration. These names are carried into the deployment manifest
      # for whoever installs the services, so the two fixed instance roles are
      # recognizable and named the same way on every machine.
      #
      # nats states the ports this machine's Event Fabric server needs open. The
      # builder joins them with the machine's ip; whichever local process holds
      # the machine fence binds them, so there is one client and at most one
      # cluster port per machine no matter how many processes are deployed.
      #
      # standby states whether a second local process is deployed to wait on that
      # fence. This sensor opts out: it is a single-purpose node whose loss is
      # already covered by the site, so a second local process would add a
      # process to operate without adding site availability. Because it opts out,
      # it must not name a standby service either.
      platform {
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
      role     = "local-server"
      ip       = "10.0.1.11"
      services = ["core-services"]
      platform {
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

          winservice {
            name         = "opdl-customer-a-north-local-server-standby"
            display_name = "OPDL customer-a north local-server (Standby Instance)"
          }
        }

        # fence is optional and almost always omitted. The machine's two processes
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
      role     = "master-server"
      ip       = "10.0.2.10"
      services = ["core-services"]
      platform {
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

          winservice {
            name         = "opdl-customer-a-control-room-master-standby"
            display_name = "OPDL customer-a control-room master (Standby Instance)"
          }
        }
      }
    }

    machine "slave" {
      role     = "slave-server"
      ip       = "10.0.2.11"
      services = ["core-services"]
      platform {
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

          winservice {
            name         = "opdl-customer-a-control-room-slave-standby"
            display_name = "OPDL customer-a control-room slave (Standby Instance)"
          }
        }
      }
    }

    machine "integration" {
      role     = "integration-server"
      ip       = "10.0.2.12"
      services = ["integration-services"]
      platform {
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

          winservice {
            name         = "opdl-customer-a-control-room-integration-standby"
            display_name = "OPDL customer-a control-room integration (Standby Instance)"
          }
        }
      }
    }
  }
}
