project "customer-a" {
  environment = "production"

  site "north" {
    machine "sensor" {
      profile  = "sensor-node"
      ip       = "10.0.1.10"
      services = ["sensor-services"]

      # Every platform block is mandatory. The blocks directly under platform are
      # the Primary Instance's; standby is the Standby Instance's. This machine
      # opts out of a standby, so it states that and authors nothing further.
      platform {
        events_file = "D:/opdl/customer-a/north/sensor/primary/events.jsonl"
        state_file  = "D:/opdl/customer-a/north/sensor/primary/state.json"

        api {
          local_port = 8080
          read_header_timeout = "5s"
          shutdown_timeout    = "10s"
        }

        winservice {
          name = "opdl-customer-a-north-sensor-primary"
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
        events_file = "D:/opdl/customer-a/north/local-server/primary/events.jsonl"
        state_file  = "D:/opdl/customer-a/north/local-server/primary/state.json"

        api {
          local_port = 8080
          read_header_timeout = "5s"
          shutdown_timeout    = "10s"
        }

        winservice {
          name = "opdl-customer-a-north-local-server-primary"
        }

        standby {
          disabled    = false
          events_file = "D:/opdl/customer-a/north/local-server/standby/events.jsonl"
          state_file  = "D:/opdl/customer-a/north/local-server/standby/state.json"

          lease {
            file                   = "D:/opdl/customer-a/north/local-server/lease"
            duration               = "15s"
            renewal_interval       = "5s"
            health_check_interval  = "2s"
            failback_stabilization = "30s"
            lag_bound              = "30s"
          }

          api {
            local_port = 8081
            read_header_timeout = "5s"
            shutdown_timeout    = "10s"
          }

          winservice {
            name = "opdl-customer-a-north-local-server-standby"
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
        events_file = "D:/opdl/customer-a/control-room/master/primary/events.jsonl"
        state_file  = "D:/opdl/customer-a/control-room/master/primary/state.json"

        api {
          local_port = 8080
          read_header_timeout = "5s"
          shutdown_timeout    = "10s"
        }

        winservice {
          name = "opdl-customer-a-control-room-master-primary"
        }

        standby {
          disabled    = false
          events_file = "D:/opdl/customer-a/control-room/master/standby/events.jsonl"
          state_file  = "D:/opdl/customer-a/control-room/master/standby/state.json"

          lease {
            file                   = "D:/opdl/customer-a/control-room/master/lease"
            duration               = "15s"
            renewal_interval       = "5s"
            health_check_interval  = "2s"
            failback_stabilization = "30s"
            lag_bound              = "30s"
          }

          api {
            local_port = 8081
            read_header_timeout = "5s"
            shutdown_timeout    = "10s"
          }

          winservice {
            name = "opdl-customer-a-control-room-master-standby"
          }
        }
      }
    }

    machine "slave" {
      profile  = "slave-server"
      ip       = "10.0.2.11"
      services = ["core-services"]
      platform {
        events_file = "D:/opdl/customer-a/control-room/slave/primary/events.jsonl"
        state_file  = "D:/opdl/customer-a/control-room/slave/primary/state.json"

        api {
          local_port = 8080
          read_header_timeout = "5s"
          shutdown_timeout    = "10s"
        }

        winservice {
          name = "opdl-customer-a-control-room-slave-primary"
        }

        standby {
          disabled    = false
          events_file = "D:/opdl/customer-a/control-room/slave/standby/events.jsonl"
          state_file  = "D:/opdl/customer-a/control-room/slave/standby/state.json"

          lease {
            file                   = "D:/opdl/customer-a/control-room/slave/lease"
            duration               = "15s"
            renewal_interval       = "5s"
            health_check_interval  = "2s"
            failback_stabilization = "30s"
            lag_bound              = "30s"
          }

          api {
            local_port = 8081
            read_header_timeout = "5s"
            shutdown_timeout    = "10s"
          }

          winservice {
            name = "opdl-customer-a-control-room-slave-standby"
          }
        }
      }
    }

    machine "integration" {
      profile  = "integration-server"
      ip       = "10.0.2.12"
      services = ["integration-services"]
      platform {
        events_file = "D:/opdl/customer-a/control-room/integration/primary/events.jsonl"
        state_file  = "D:/opdl/customer-a/control-room/integration/primary/state.json"

        api {
          local_port = 8080
          read_header_timeout = "5s"
          shutdown_timeout    = "10s"
        }

        winservice {
          name = "opdl-customer-a-control-room-integration-primary"
        }

        standby {
          disabled    = false
          events_file = "D:/opdl/customer-a/control-room/integration/standby/events.jsonl"
          state_file  = "D:/opdl/customer-a/control-room/integration/standby/state.json"

          lease {
            file                   = "D:/opdl/customer-a/control-room/integration/lease"
            duration               = "15s"
            renewal_interval       = "5s"
            health_check_interval  = "2s"
            failback_stabilization = "30s"
            lag_bound              = "30s"
          }

          api {
            local_port = 8081
            read_header_timeout = "5s"
            shutdown_timeout    = "10s"
          }

          winservice {
            name = "opdl-customer-a-control-room-integration-standby"
          }
        }
      }
    }
  }
}
