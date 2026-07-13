project "customer-a" {
  environment = "production"

  database {
    provider = "postgres"
    host     = "db.customer-a.local"
    port     = 5432
  }

  features {
    opcua     = true
    historian = true
  }

  site "north" {
    machine "sensor-01" {
      role     = "sensor-node"
      services = ["sensor-service", "opcua-adapter"]

      service "opcua-adapter" {
        endpoint  = "opc.tcp://plc-north-01:4840"
        namespace = "2"
        interval  = "1s"
      }
    }

    machine "historian-01" {
      role     = "historian-node"
      services = ["historian"]
    }
  }
}
