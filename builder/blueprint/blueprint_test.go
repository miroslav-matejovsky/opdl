package blueprint_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/builder/blueprint"
)

// writeBlueprint writes body to a project.hcl in a fresh temp dir and returns it.
func writeBlueprint(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.hcl"), []byte(body), 0o644))
	return dir
}

const validBody = `
project "customer-a" {
  environment = "production"
  database {
    provider = "postgres"
    host     = "db"
    port     = 5432
  }
  features {
    opcua     = true
    alarms    = true
    historian = true
  }
  site "north" {
    machine "sensor-01" {
      role     = "sensor-node"
      services = ["sensor-service", "opcua-adapter"]
      service "opcua-adapter" {
        endpoint = "opc.tcp://plc:4840"
      }
    }
    machine "historian-01" {
      role     = "historian-node"
      services = ["historian"]
    }
  }
}
`

func TestLoadValid(t *testing.T) {
	p, err := blueprint.Load(writeBlueprint(t, validBody))
	require.NoError(t, err)
	require.Equal(t, "customer-a", p.Name)
	require.Len(t, p.Sites, 1)
	require.Len(t, p.Sites[0].Machines, 2)
}

func TestLoadUnknownService(t *testing.T) {
	body := `
project "c" {
  environment = "e"
  database {
    provider = "sqlite"
    host     = "local"
  }
  features {}
  site "s" {
    machine "m" {
      role     = "r"
      services = ["ghost-service"]
    }
  }
}`
	_, err := blueprint.Load(writeBlueprint(t, body))
	require.ErrorContains(t, err, "unknown service")
}

func TestLoadFeatureGate(t *testing.T) {
	body := `
project "c" {
  environment = "e"
  database {
    provider = "sqlite"
    host     = "local"
  }
  features {
    opcua = false
  }
  site "s" {
    machine "m" {
      role     = "r"
      services = ["opcua-adapter"]
    }
  }
}`
	_, err := blueprint.Load(writeBlueprint(t, body))
	require.ErrorContains(t, err, "requires feature")
}

func TestLoadDuplicateMachine(t *testing.T) {
	body := `
project "c" {
  environment = "e"
  database {
    provider = "sqlite"
    host     = "local"
  }
  features {}
  site "s1" {
    machine "m" {
      role     = "r"
      services = ["sensor-service"]
    }
  }
  site "s2" {
    machine "m" {
      role     = "r"
      services = ["sensor-service"]
    }
  }
}`
	_, err := blueprint.Load(writeBlueprint(t, body))
	require.ErrorContains(t, err, "duplicate machine")
}

func TestLoadNoDir(t *testing.T) {
	_, err := blueprint.Load(filepath.Join(t.TempDir(), "missing"))
	require.Error(t, err)
}
