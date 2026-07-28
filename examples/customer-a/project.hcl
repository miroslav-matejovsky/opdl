# Example blueprint describing project "customer-a".
#
# This file is an illustrative topology showing how a production deployment is
# authored. It is valid HCL that decodes and validates cleanly with the builder.

project "customer-a" {
  environment = "production"

  site "north" {
    machine "sensor" {
      profile         = "sensor-node"
      ip              = "10.0.1.10"
      eventstore_file = "D:/opdl/customer-a/north/sensor/machine-events.jsonl"

      # Every service the machine hosts declares the part this copy of it plays
      # — master or slave — and the probe that says whether it is up: what to
      # ask, how often, and how many consecutive failures make it down.
      service "sensor-services" {
        role = "master"

        health_check {
          type     = "http"
          port     = 9101
          path     = "/health"
          interval = "10s"
          timeout  = "2s"
          retries  = 3
        }
      }

      primary {
        eventlog_file = "D:/opdl/customer-a/north/sensor/primary/events.jsonl"
        state_file    = "D:/opdl/customer-a/north/sensor/primary/state.json"
        log_file      = "D:/opdl/customer-a/north/sensor/primary/platform.log"

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
      eventstore_file = "D:/opdl/customer-a/north/local-server/machine-events.jsonl"

      # The control room holds the master copy of the core services; this site
      # follows it.
      service "core-services" {
        role = "slave"

        health_check {
          type     = "http"
          port     = 9101
          path     = "/health"
          interval = "10s"
          timeout  = "2s"
          retries  = 3
        }
      }

      # A machine hosts as many services as it needs; each states its own role
      # and its own probe, on its own port.
      service "alarm-service" {
        role = "master"

        health_check {
          type     = "http"
          port     = 9102
          path     = "/health"
          interval = "5s"
          timeout  = "1s"
          retries  = 2
        }
      }

      primary {
        eventlog_file = "D:/opdl/customer-a/north/local-server/primary/events.jsonl"
        state_file    = "D:/opdl/customer-a/north/local-server/primary/state.json"
        log_file      = "D:/opdl/customer-a/north/local-server/primary/platform.log"

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
        log_file      = "D:/opdl/customer-a/north/local-server/standby/platform.log"

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
      eventstore_file = "D:/opdl/customer-a/control-room/master/machine-events.jsonl"

      service "core-services" {
        role = "master"

        health_check {
          type     = "http"
          port     = 9101
          path     = "/health"
          interval = "10s"
          timeout  = "2s"
          retries  = 3
        }
      }

      primary {
        eventlog_file = "D:/opdl/customer-a/control-room/master/primary/events.jsonl"
        state_file    = "D:/opdl/customer-a/control-room/master/primary/state.json"
        log_file      = "D:/opdl/customer-a/control-room/master/primary/platform.log"

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
        log_file      = "D:/opdl/customer-a/control-room/master/standby/platform.log"

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
      eventstore_file = "D:/opdl/customer-a/control-room/slave/machine-events.jsonl"

      service "core-services" {
        role = "slave"

        health_check {
          type     = "http"
          port     = 9101
          path     = "/health"
          interval = "10s"
          timeout  = "2s"
          retries  = 3
        }
      }

      primary {
        eventlog_file = "D:/opdl/customer-a/control-room/slave/primary/events.jsonl"
        state_file    = "D:/opdl/customer-a/control-room/slave/primary/state.json"
        log_file      = "D:/opdl/customer-a/control-room/slave/primary/platform.log"

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
        log_file      = "D:/opdl/customer-a/control-room/slave/standby/platform.log"

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
      eventstore_file = "D:/opdl/customer-a/control-room/integration/machine-events.jsonl"

      # Every service states a probe. What differs is how hard it presses: these
      # services reach outward, so they are given longer to answer and more
      # attempts before they are called down.
      service "integration-services" {
        role = "master"

        health_check {
          type     = "http"
          port     = 9101
          path     = "/health/ready"
          interval = "30s"
          timeout  = "5s"
          retries  = 5
        }
      }

      primary {
        eventlog_file = "D:/opdl/customer-a/control-room/integration/primary/events.jsonl"
        state_file    = "D:/opdl/customer-a/control-room/integration/primary/state.json"
        log_file      = "D:/opdl/customer-a/control-room/integration/primary/platform.log"

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
        log_file      = "D:/opdl/customer-a/control-room/integration/standby/platform.log"

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
