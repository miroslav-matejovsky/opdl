package blueprint_test

import (
	"testing"

	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/builder/internal/blueprint"
)

func validProject() *blueprint.Project {
	return &blueprint.Project{
		Name:        "customer-a",
		Environment: "production",
		Features:    blueprint.Features{Chaos: true},
		Sites: []blueprint.Site{{
			Name: "north",
			Machines: []blueprint.Machine{{
				Name:     "sensor",
				Role:     "sensor-node",
				IP:       "10.0.1.10",
				Services: []string{"sensor-services"},
				Platform: []blueprint.Platform{{Instances: []blueprint.PlatformInstance{
					{Name: "primary", APIAddress: "10.0.1.10:8080", FabricClientAddress: "10.0.1.10:3320", FabricMemberlistAddress: "10.0.1.10:3322"},
					{Name: "secondary", APIAddress: "10.0.1.10:8081", FabricClientAddress: "10.0.1.10:3321", FabricMemberlistAddress: "10.0.1.10:3323"},
				}}},
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
			Platform: []blueprint.Platform{{Instances: []blueprint.PlatformInstance{
				{Name: "primary", APIAddress: "10.0.1.12:8080", FabricClientAddress: "10.0.1.12:3320", FabricMemberlistAddress: "10.0.1.12:3322"},
				{Name: "secondary", APIAddress: "10.0.1.12:8081", FabricClientAddress: "10.0.1.12:3321", FabricMemberlistAddress: "10.0.1.12:3323"},
			}}},
		}},
	})
	require.ErrorContains(t, p.Validate(), "duplicate site")
}

// TestProjectValidateDuplicateIP checks an IP identifies exactly one machine.
// Machine identity remains unique even though process endpoints are explicit.
func TestProjectValidateDuplicateIP(t *testing.T) {
	t.Run("within one site", func(t *testing.T) {
		p := validProject()
		p.Sites[0].Machines = append(p.Sites[0].Machines, blueprint.Machine{
			Name:     "gateway",
			Role:     "gateway-node",
			IP:       "10.0.1.10",
			Services: []string{"core-services"},
			Platform: validProject().Sites[0].Machines[0].Platform,
		})
		require.ErrorContains(t, p.Validate(), `machines "sensor" and "gateway" share ip "10.0.1.10"`)
	})

	t.Run("across sites", func(t *testing.T) {
		p := validProject()
		p.Sites = append(p.Sites, blueprint.Site{
			Name: "south",
			Machines: []blueprint.Machine{{
				Name:     "south-node",
				Role:     "sensor-node",
				IP:       "10.0.1.10",
				Services: []string{"sensor-services"},
				Platform: validProject().Sites[0].Machines[0].Platform,
			}},
		})
		require.ErrorContains(t, p.Validate(), `share ip "10.0.1.10"`)
	})
}

func TestFeatures(t *testing.T) {
	f := blueprint.Features{Chaos: true}
	require.True(t, f.Chaos)
}

func TestPlatformSecondaryDefaultsEnabled(t *testing.T) {
	p := validProject()
	require.True(t, p.Sites[0].Machines[0].Platform[0].SecondaryIsEnabled())
	require.Equal(t, []string{"primary", "secondary"}, []string{
		p.Sites[0].Machines[0].PlatformInstances()[0].Name,
		p.Sites[0].Machines[0].PlatformInstances()[1].Name,
	})
}

func TestPlatformSecondaryCanBeDisabled(t *testing.T) {
	p := validProject()
	disabled := false
	p.Sites[0].Machines[0].Platform[0].SecondaryEnabled = &disabled
	p.Sites[0].Machines[0].Platform[0].Instances = p.Sites[0].Machines[0].Platform[0].Instances[:1]
	require.NoError(t, p.Validate())
	require.Len(t, p.Sites[0].Machines[0].PlatformInstances(), 1)
}

func TestPlatformValidationFailures(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*blueprint.Machine)
		errText string
	}{
		{"missing platform block", func(m *blueprint.Machine) { m.Platform = nil }, "exactly one platform block is required"},
		{"missing primary", func(m *blueprint.Machine) { m.Platform[0].Instances = m.Platform[0].Instances[1:] }, "primary platform instance is required"},
		{"missing enabled secondary", func(m *blueprint.Machine) { m.Platform[0].Instances = m.Platform[0].Instances[:1] }, "secondary platform instance is enabled but not defined"},
		{"missing endpoint", func(m *blueprint.Machine) { m.Platform[0].Instances[0].APIAddress = "" }, "api_address"},
		{"invalid endpoint port", func(m *blueprint.Machine) { m.Platform[0].Instances[0].APIAddress = "10.0.1.10:70000" }, "out of range"},
		{"duplicate endpoint", func(m *blueprint.Machine) {
			m.Platform[0].Instances[1].APIAddress = m.Platform[0].Instances[0].APIAddress
		}, "already used"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := validProject()
			test.mutate(&p.Sites[0].Machines[0])
			require.ErrorContains(t, p.Validate(), test.errText)
		})
	}
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

func TestProjectHCLRejectsRemovedRedundancyFeature(t *testing.T) {
	parser := hclparse.NewParser()
	file, diags := parser.ParseHCL([]byte(`project "old" {
	  environment = "production"
	  features { redundancy = true }
	}`), "test.hcl")
	require.False(t, diags.HasErrors(), diags.Error())

	var tf topologyFile
	diags = gohcl.DecodeBody(file.Body, nil, &tf)
	require.True(t, diags.HasErrors())
	require.Contains(t, diags.Error(), "Unsupported argument")
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
			      platform {
			        instance "primary" {
			          api_address = "10.0.1.10:8080"
			          fabric_client_address = "10.0.1.10:3320"
			          fabric_memberlist_address = "10.0.1.10:3322"
			        }
			        instance "secondary" {
			          api_address = "10.0.1.10:8081"
			          fabric_client_address = "10.0.1.10:3321"
			          fabric_memberlist_address = "10.0.1.10:3323"
			        }
			      }
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
