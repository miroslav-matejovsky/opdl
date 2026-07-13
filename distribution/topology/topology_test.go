package topology_test

import (
	"os"
	"testing"

	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/distribution/catalog"
	topology "github.com/miroslav-matejovsky/opdl/distribution/topology"
)

func validProject() *topology.Project {
	return &topology.Project{
		Name:        "customer-a",
		Environment: "production",
		Database:    topology.Database{Provider: "postgres", Host: "db", Port: 5432},
		Features:    topology.Features{OPCUA: true, Historian: true},
		Sites: []topology.Site{{
			Name: "north",
			Machines: []topology.Machine{{
				Name:     "sensor-01",
				Role:     "sensor-node",
				Services: []string{catalog.ServiceSensor, catalog.ServiceOPCUAAdapter},
				Assignments: []topology.ServiceAssignment{
					{Name: catalog.ServiceOPCUAAdapter, Endpoint: "opc.tcp://plc:4840"},
				},
			}},
		}},
	}
}

func TestProjectValidateOK(t *testing.T) {
	require.NoError(t, validProject().Validate())
}

func TestProjectValidateUnknownService(t *testing.T) {
	p := validProject()
	p.Sites[0].Machines[0].Services = []string{"ghost-service"}
	require.ErrorContains(t, p.Validate(), "unknown service")
}

func TestProjectValidateFeatureGate(t *testing.T) {
	p := validProject()
	p.Features.OPCUA = false // opcua-adapter now ungated
	require.ErrorContains(t, p.Validate(), "requires feature")
}

func TestProjectValidateDuplicateMachine(t *testing.T) {
	p := validProject()
	p.Sites[0].Machines = append(p.Sites[0].Machines, p.Sites[0].Machines[0])
	require.ErrorContains(t, p.Validate(), "duplicate machine")
}

func TestFeaturesEnabled(t *testing.T) {
	f := topology.Features{OPCUA: true, Chaos: true}
	require.True(t, f.Enabled(catalog.FeatureOPCUA))
	require.True(t, f.Enabled(catalog.FeatureChaos))
	require.False(t, f.Enabled(catalog.FeatureHistorian))
	require.False(t, f.Enabled("nonsense"))
}

func TestMachineLookupAssignment(t *testing.T) {
	m := validProject().Sites[0].Machines[0]
	a, ok := m.LookupAssignment(catalog.ServiceOPCUAAdapter)
	require.True(t, ok)
	require.Equal(t, "opc.tcp://plc:4840", a.Endpoint)
	_, ok = m.LookupAssignment(catalog.ServiceHistorian)
	require.False(t, ok)
}

func TestProjectValidateSingleActiveStaticMembers(t *testing.T) {
	p := validProject()
	p.Sites[0].Machines = []topology.Machine{
		{
			Name: "sensor-01", Role: "sensor-node", Services: []string{catalog.ServiceSensor},
			Assignments: []topology.ServiceAssignment{{Name: catalog.ServiceSensor, Redundancy: &topology.RedundancyPolicy{
				Mode: "single-active", Group: "sensors", Scope: "site", Members: []string{"sensor-01", "sensor-02"},
				LeaseDuration: "5s", RetryInitial: "100ms", RetryMax: "1s", FailoverTimeout: "10s", DrainTimeout: "1s",
			}}},
		},
		{
			Name: "sensor-02", Role: "sensor-node", Services: []string{catalog.ServiceSensor},
			Assignments: []topology.ServiceAssignment{{Name: catalog.ServiceSensor, Redundancy: &topology.RedundancyPolicy{
				Mode: "single-active", Group: "sensors", Scope: "site", Members: []string{"sensor-01", "sensor-02"},
				LeaseDuration: "5s", RetryInitial: "100ms", RetryMax: "1s", FailoverTimeout: "10s", DrainTimeout: "1s",
			}}},
		},
	}
	require.NoError(t, p.Validate())

	p.Sites[0].Machines[1].Assignments[0].Redundancy.Group = "other"
	require.ErrorContains(t, p.Validate(), "matching policy")
}

type topologyFile struct {
	Projects []topology.Project `hcl:"project,block"`
}

func decodeHCL(t *testing.T, src string) topology.Project {
	t.Helper()
	parser := hclparse.NewParser()
	file, diags := parser.ParseHCL([]byte(src), "test.hcl")
	require.False(t, diags.HasErrors(), diags.Error())

	var tf topologyFile
	diags = gohcl.DecodeBody(file.Body, nil, &tf)
	require.False(t, diags.HasErrors(), diags.Error())
	require.Len(t, tf.Projects, 1)
	return tf.Projects[0]
}

func TestProjectHCLDecodeAndValidate(t *testing.T) {
	data, err := os.ReadFile("testdata/project.hcl")
	require.NoError(t, err)

	p := decodeHCL(t, string(data))
	require.Equal(t, "customer-a", p.Name)
	require.Equal(t, "production", p.Environment)
	require.Equal(t, "postgres", p.Database.Provider)
	require.Equal(t, "db.customer-a.local", p.Database.Host)
	require.Equal(t, 5432, p.Database.Port)

	require.True(t, p.Features.Enabled(catalog.FeatureOPCUA))
	require.True(t, p.Features.Enabled(catalog.FeatureHistorian))
	require.False(t, p.Features.Enabled(catalog.FeatureAlarms))

	require.Len(t, p.Sites, 1)
	require.Equal(t, "north", p.Sites[0].Name)
	require.Len(t, p.Sites[0].Machines, 2)

	m1 := p.Sites[0].Machines[0]
	require.Equal(t, "sensor-01", m1.Name)
	require.Equal(t, "sensor-node", m1.Role)
	require.Equal(t, []string{"sensor-service", "opcua-adapter"}, m1.Services)

	a, ok := m1.LookupAssignment(catalog.ServiceOPCUAAdapter)
	require.True(t, ok)
	require.Equal(t, "opc.tcp://plc-north-01:4840", a.Endpoint)
	require.Equal(t, "2", a.Namespace)
	require.Equal(t, "1s", a.Interval)

	require.NoError(t, p.Validate())
}

func TestProjectHCLValidationFailures(t *testing.T) {
	tests := []struct {
		name    string
		hcl     string
		errText string
	}{
		{
			name: "unknown service",
			hcl: `project "bad-service" {
			  environment = "production"
			  database {
			    provider = "postgres"
			    host     = "db"
			  }
			  features {}
			  site "north" {
			    machine "m1" {
			      role     = "node"
			      services = ["unknown-service"]
			    }
			  }
			}`,
			errText: "unknown service",
		},
		{
			name: "feature gate violation",
			hcl: `project "gated" {
			  environment = "production"
			  database {
			    provider = "postgres"
			    host     = "db"
			  }
			  features {}
			  site "north" {
			    machine "m1" {
			      role     = "node"
			      services = ["opcua-adapter"]
			    }
			  }
			}`,
			errText: "requires feature",
		},
		{
			name: "duplicate machine name across sites",
			hcl: `project "dup-machine" {
			  environment = "production"
			  database {
			    provider = "postgres"
			    host     = "db"
			  }
			  features {}
			  site "north" {
			    machine "node-1" {
			      role     = "node"
			      services = ["sensor-service"]
			    }
			  }
			  site "south" {
			    machine "node-1" {
			      role     = "node"
			      services = ["sensor-service"]
			    }
			  }
			}`,
			errText: "duplicate machine",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := decodeHCL(t, tc.hcl)
			require.ErrorContains(t, p.Validate(), tc.errText)
		})
	}
}
