project "customer-a" {
  environment = "production"

  site "north" {
    machine "sensor" {
      profile         = "sensor-node"
      ip              = "10.0.1.10"
      services        = ["sensor-services"]
      eventstore_file = "D:/opdl/customer-a/north/sensor/machine-events.jsonl"

      primary {
        eventlog_file = "D:/opdl/customer-a/north/sensor/primary/events.jsonl"
        state_file    = "D:/opdl/customer-a/north/sensor/primary/state.json"

        api {
          local_port          = 8080
          read_header_timeout = "5s"
          shutdown_timeout    = "10s"
        }

        winservice {
          name         = "opdl-customer-a-north-sensor-primary"
          display_name = "OPDL customer-a north sensor (Primary Instance)"
          description  = "OPDL platform Primary Instance for machine sensor."
        }
      }

      standby {
        disabled = true
      }
    }

    machine "local-server" {
      profile         = "local-server"
      ip              = "10.0.1.11"
      services        = ["core-services"]
      eventstore_file = "D:/opdl/customer-a/north/local-server/machine-events.jsonl"

      primary {
        eventlog_file = "D:/opdl/customer-a/north/local-server/primary/events.jsonl"
        state_file    = "D:/opdl/customer-a/north/local-server/primary/state.json"

        api {
          local_port          = 8080
          read_header_timeout = "5s"
          shutdown_timeout    = "10s"
        }

        winservice {
          name         = "opdl-customer-a-north-local-server-primary"
          display_name = "OPDL customer-a north local-server (Primary Instance)"
        }
      }

      standby {
        disabled      = false
        eventlog_file = "D:/opdl/customer-a/north/local-server/standby/events.jsonl"
        state_file    = "D:/opdl/customer-a/north/local-server/standby/state.json"

        lease {
          file                   = "D:/opdl/customer-a/north/local-server/lease"
          duration               = "15s"
          renewal_interval       = "5s"
          health_check_interval  = "2s"
          failback_stabilization = "30s"
          lag_bound              = "30s"
        }

        api {
          local_port          = 8081
          read_header_timeout = "5s"
          shutdown_timeout    = "10s"
        }

        winservice {
          name         = "opdl-customer-a-north-local-server-standby"
          display_name = "OPDL customer-a north local-server (Standby Instance)"
        }
      }
    }
  }

  site "control-room" {
    machine "master" {
      profile         = "master-server"
      ip              = "10.0.2.10"
      services        = ["core-services"]
      eventstore_file = "D:/opdl/customer-a/control-room/master/machine-events.jsonl"

      primary {
        eventlog_file = "D:/opdl/customer-a/control-room/master/primary/events.jsonl"
        state_file    = "D:/opdl/customer-a/control-room/master/primary/state.json"

        api {
          local_port          = 8080
          read_header_timeout = "5s"
          shutdown_timeout    = "10s"
        }

        winservice {
          name         = "opdl-customer-a-control-room-master-primary"
          display_name = "OPDL customer-a control-room master (Primary Instance)"
        }
      }

      standby {
        disabled      = false
        eventlog_file = "D:/opdl/customer-a/control-room/master/standby/events.jsonl"
        state_file    = "D:/opdl/customer-a/control-room/master/standby/state.json"

        lease {
          file                   = "D:/opdl/customer-a/control-room/master/lease"
          duration               = "15s"
          renewal_interval       = "5s"
          health_check_interval  = "2s"
          failback_stabilization = "30s"
          lag_bound              = "30s"
        }

        api {
          local_port          = 8081
          read_header_timeout = "5s"
          shutdown_timeout    = "10s"
        }

        winservice {
          name         = "opdl-customer-a-control-room-master-standby"
          display_name = "OPDL customer-a control-room master (Standby Instance)"
        }
      }
    }

    machine "slave" {
      profile         = "slave-server"
      ip              = "10.0.2.11"
      services        = ["core-services"]
      eventstore_file = "D:/opdl/customer-a/control-room/slave/machine-events.jsonl"

      primary {
        eventlog_file = "D:/opdl/customer-a/control-room/slave/primary/events.jsonl"
        state_file    = "D:/opdl/customer-a/control-room/slave/primary/state.json"

        api {
          local_port          = 8080
          read_header_timeout = "5s"
          shutdown_timeout    = "10s"
        }

        winservice {
          name         = "opdl-customer-a-control-room-slave-primary"
          display_name = "OPDL customer-a control-room slave (Primary Instance)"
        }
      }

      standby {
        disabled      = false
        eventlog_file = "D:/opdl/customer-a/control-room/slave/standby/events.jsonl"
        state_file    = "D:/opdl/customer-a/control-room/slave/standby/state.json"

        lease {
          file                   = "D:/opdl/customer-a/control-room/slave/lease"
          duration               = "15s"
          renewal_interval       = "5s"
          health_check_interval  = "2s"
          failback_stabilization = "30s"
          lag_bound              = "30s"
        }

        api {
          local_port          = 8081
          read_header_timeout = "5s"
          shutdown_timeout    = "10s"
        }

        winservice {
          name         = "opdl-customer-a-control-room-slave-standby"
          display_name = "OPDL customer-a control-room slave (Standby Instance)"
        }
      }
    }

    machine "integration" {
      profile         = "integration-server"
      ip              = "10.0.2.12"
      services        = ["integration-services"]
      eventstore_file = "D:/opdl/customer-a/control-room/integration/machine-events.jsonl"

      primary {
        eventlog_file = "D:/opdl/customer-a/control-room/integration/primary/events.jsonl"
        state_file    = "D:/opdl/customer-a/control-room/integration/primary/state.json"

        api {
          local_port          = 8080
          read_header_timeout = "5s"
          shutdown_timeout    = "10s"
        }

        winservice {
          name         = "opdl-customer-a-control-room-integration-primary"
          display_name = "OPDL customer-a control-room integration (Primary Instance)"
        }
      }

      standby {
        disabled      = false
        eventlog_file = "D:/opdl/customer-a/control-room/integration/standby/events.jsonl"
        state_file    = "D:/opdl/customer-a/control-room/integration/standby/state.json"

        lease {
          file                   = "D:/opdl/customer-a/control-room/integration/lease"
          duration               = "15s"
          renewal_interval       = "5s"
          health_check_interval  = "2s"
          failback_stabilization = "30s"
          lag_bound              = "30s"
        }

        api {
          local_port          = 8081
          read_header_timeout = "5s"
          shutdown_timeout    = "10s"
        }

        winservice {
          name         = "opdl-customer-a-control-room-integration-standby"
          display_name = "OPDL customer-a control-room integration (Standby Instance)"
        }
      }
    }
  }
}
