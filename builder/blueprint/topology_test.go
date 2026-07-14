package blueprint_test

import (
	"os"
	"testing"

	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/builder/blueprint"
)

func validProject() *blueprint.Project {
	return &blueprint.Project{
		Name:        "customer-a",
		Environment: "production",
		Features:    blueprint.Features{Chaos: true, Redundancy: true},
		Sites: []blueprint.Site{{
			Name: "north",
			Machines: []blueprint.Machine{{
				Name:     "sensor",
				Role:     "sensor-node",
				IP:       "10.0.1.10",
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

func TestProjectValidateMissingIP(t *testing.T) {
	p := validProject()
	p.Sites[0].Machines[0].IP = ""
	require.ErrorContains(t, p.Validate(), "ip is required")
}

func TestProjectValidateInvalidIP(t *testing.T) {
	p := validProject()
	p.Sites[0].Machines[0].IP = "not-an-ip"
	require.ErrorContains(t, p.Validate(), "not a valid IP address")
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
	p.Sites = append(p.Sites, blueprint.Site{
		Name: "north",
		Machines: []blueprint.Machine{{
			Name:     "other",
			Role:     "sensor-node",
			IP:       "10.0.1.12",
			Services: []string{"sensor-services"},
		}},
	})
	require.ErrorContains(t, p.Validate(), "duplicate site")
}

func TestFeatures(t *testing.T) {
	f := blueprint.Features{Chaos: true}
	require.True(t, f.Chaos)
	require.False(t, f.Redundancy)
}

type topologyFile struct {
	Projects []blueprint.Project `hcl:"project,block"`
}

func decodeHCL(t *testing.T, src string) blueprint.Project {
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
	require.Equal(t, "10.0.1.10", m1.IP)
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
			      ip       = "10.0.1.10"
			      services = ["core-services"]
			    }
			  }
			}`,
			errText: "role is required",
		},
		{
			name: "missing ip",
			hcl: `project "bad-ip" {
			  environment = "production"
			  features {}
			  site "north" {
			    machine "m1" {
			      role     = "node"
			      ip       = ""
			      services = ["core-services"]
			    }
			  }
			}`,
			errText: "ip is required",
		},
		{
			name: "invalid ip",
			hcl: `project "bad-ip" {
			  environment = "production"
			  features {}
			  site "north" {
			    machine "m1" {
			      role     = "node"
			      ip       = "not-an-ip"
			      services = ["core-services"]
			    }
			  }
			}`,
			errText: "not a valid IP address",
		},
		{
			name: "duplicate machine name across sites",
			hcl: `project "dup-machine" {
			  environment = "production"
			  features {}
			  site "north" {
			    machine "node-1" {
			      role     = "node"
			      ip       = "10.0.1.10"
			      services = ["core-services"]
			    }
			  }
			  site "south" {
			    machine "node-1" {
			      role     = "node"
			      ip       = "10.0.1.11"
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
