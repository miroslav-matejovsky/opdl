package topology_test

import (
	"os"
	"testing"

	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/stretchr/testify/require"

	topology "github.com/miroslav-matejovsky/opdl/distribution/topology"
)

func validProject() *topology.Project {
	return &topology.Project{
		Name:        "customer-a",
		Environment: "production",
		Features:    topology.Features{Chaos: true, Redundancy: true},
		Sites: []topology.Site{{
			Name: "north",
			Machines: []topology.Machine{{
				Name:     "sensor",
				Role:     "sensor-node",
				Services: []string{"sensor-services"},
			}},
		}},
	}
}

func TestProjectValidateOK(t *testing.T) {
	require.NoError(t, validProject().Validate())
}

func TestProjectValidateMissingName(t *testing.T) {
	p := validProject()
	p.Name = ""
	require.ErrorContains(t, p.Validate(), "name is required")
}

func TestProjectValidateMissingEnvironment(t *testing.T) {
	p := validProject()
	p.Environment = ""
	require.ErrorContains(t, p.Validate(), "environment is required")
}

func TestProjectValidateMissingRole(t *testing.T) {
	p := validProject()
	p.Sites[0].Machines[0].Role = ""
	require.ErrorContains(t, p.Validate(), "role is required")
}

func TestProjectValidateEmptyServices(t *testing.T) {
	p := validProject()
	p.Sites[0].Machines[0].Services = nil
	require.ErrorContains(t, p.Validate(), "at least one service is required")
}

func TestProjectValidateDuplicateService(t *testing.T) {
	p := validProject()
	p.Sites[0].Machines[0].Services = []string{"sensor-services", "sensor-services"}
	require.ErrorContains(t, p.Validate(), "assigned more than once")
}

func TestProjectValidateDuplicateMachine(t *testing.T) {
	p := validProject()
	p.Sites[0].Machines = append(p.Sites[0].Machines, p.Sites[0].Machines[0])
	require.ErrorContains(t, p.Validate(), "duplicate machine")
}

func TestProjectValidateDuplicateSite(t *testing.T) {
	p := validProject()
	p.Sites = append(p.Sites, topology.Site{
		Name: "north",
		Machines: []topology.Machine{{
			Name:     "other",
			Role:     "sensor-node",
			Services: []string{"sensor-services"},
		}},
	})
	require.ErrorContains(t, p.Validate(), "duplicate site")
}

func TestFeatures(t *testing.T) {
	f := topology.Features{Chaos: true}
	require.True(t, f.Chaos)
	require.False(t, f.Redundancy)
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
	require.True(t, p.Features.Chaos)
	require.True(t, p.Features.Redundancy)

	require.Len(t, p.Sites, 2)
	require.Equal(t, "north", p.Sites[0].Name)
	require.Len(t, p.Sites[0].Machines, 2)

	m1 := p.Sites[0].Machines[0]
	require.Equal(t, "sensor", m1.Name)
	require.Equal(t, "sensor-node", m1.Role)
	require.Equal(t, []string{"sensor-services"}, m1.Services)

	require.Equal(t, "control-room", p.Sites[1].Name)
	require.Len(t, p.Sites[1].Machines, 3)

	require.NoError(t, p.Validate())
}

func TestProjectHCLValidationFailures(t *testing.T) {
	tests := []struct {
		name    string
		hcl     string
		errText string
	}{
		{
			name: "missing role",
			hcl: `project "bad-role" {
			  environment = "production"
			  features {}
			  site "north" {
			    machine "m1" {
			      role     = ""
			      services = ["core-services"]
			    }
			  }
			}`,
			errText: "role is required",
		},
		{
			name: "duplicate machine name across sites",
			hcl: `project "dup-machine" {
			  environment = "production"
			  features {}
			  site "north" {
			    machine "node-1" {
			      role     = "node"
			      services = ["core-services"]
			    }
			  }
			  site "south" {
			    machine "node-1" {
			      role     = "node"
			      services = ["core-services"]
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
