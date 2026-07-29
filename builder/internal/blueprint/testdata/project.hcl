project "customer-a" {
  environment = "production"

  site "north" {
    nats {
      cluster_name = "customer-a-north"
    }

    machine "sensor" {
      profile         = "sensor-node"
      ip              = "10.0.1.10"
      eventstore_file = "D:/opdl/customer-a/north/sensor/machine-events.jsonl"

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

        nats {
          cluster_port = 6222
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

        nats {
          cluster_port = 6222
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
        }

        api {
          local_port          = 8081
          read_header_timeout = "5s"
          shutdown_timeout    = "10s"
        }

        nats {
          cluster_port = 6223
        }

        winservice {
          name         = "opdl-customer-a-north-local-server-standby"
          display_name = "OPDL customer-a north local-server (Standby Instance)"
        }
      }
    }
  }

  site "control-room" {
    nats {
      cluster_name = "customer-a-control-room"
    }

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

        nats {
          cluster_port = 6222
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
        }

        api {
          local_port          = 8081
          read_header_timeout = "5s"
          shutdown_timeout    = "10s"
        }

        nats {
          cluster_port = 6223
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

        nats {
          cluster_port = 6222
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
        }

        api {
          local_port          = 8081
          read_header_timeout = "5s"
          shutdown_timeout    = "10s"
        }

        nats {
          cluster_port = 6223
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

      # The integration services reach outward, so their probe is slower and more
      # forgiving than a local one. Every service states a probe; what differs
      # between them is how hard it presses.
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

        nats {
          cluster_port = 6222
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
        }

        api {
          local_port          = 8081
          read_header_timeout = "5s"
          shutdown_timeout    = "10s"
        }

        nats {
          cluster_port = 6223
        }

        winservice {
          name         = "opdl-customer-a-control-room-integration-standby"
          display_name = "OPDL customer-a control-room integration (Standby Instance)"
        }
      }
    }
  }
}
