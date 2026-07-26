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
        data_dir    = "D:/opdl/customer-a/north/sensor/primary"

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
        data_dir    = "D:/opdl/customer-a/north/local-server/primary"

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
          data_dir    = "D:/opdl/customer-a/north/local-server/standby"

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
        data_dir    = "D:/opdl/customer-a/control-room/master/primary"

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
          data_dir    = "D:/opdl/customer-a/control-room/master/standby"

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
        data_dir    = "D:/opdl/customer-a/control-room/slave/primary"

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
          data_dir    = "D:/opdl/customer-a/control-room/slave/standby"

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
        data_dir    = "D:/opdl/customer-a/control-room/integration/primary"

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
          data_dir    = "D:/opdl/customer-a/control-room/integration/standby"

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
