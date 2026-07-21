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
				Platform: &blueprint.Platform{
					Nats:    &blueprint.Nats{ClientPort: 4222, ClusterPort: 6222},
					Standby: &blueprint.Standby{Disabled: false},
				},
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
			Platform: &blueprint.Platform{
				Nats:    &blueprint.Nats{ClientPort: 4222, ClusterPort: 6222},
				Standby: &blueprint.Standby{Disabled: false},
			},
		}},
	})
	require.ErrorContains(t, p.Validate(), "duplicate site")
}

// TestProjectValidateDuplicateIP checks an IP identifies exactly one machine.
// The platform derives its fabric addresses from a machine's IP on fixed ports,
// so two machines sharing one would derive the same addresses.
func TestProjectValidateDuplicateIP(t *testing.T) {
	t.Run("within one site", func(t *testing.T) {
		p := validProject()
		p.Sites[0].Machines = append(p.Sites[0].Machines, blueprint.Machine{
			Name:     "gateway",
			Role:     "gateway-node",
			IP:       "10.0.1.10",
			Services: []string{"core-services"},
			Platform: &blueprint.Platform{
				Nats:    &blueprint.Nats{ClientPort: 4222, ClusterPort: 6222},
				Standby: &blueprint.Standby{Disabled: false},
			},
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
				Platform: &blueprint.Platform{
					Nats:    &blueprint.Nats{ClientPort: 4222, ClusterPort: 6222},
					Standby: &blueprint.Standby{Disabled: false},
				},
			}},
		})
		require.ErrorContains(t, p.Validate(), `share ip "10.0.1.10"`)
	})
}

func TestFeatures(t *testing.T) {
	f := blueprint.Features{Chaos: true}
	require.True(t, f.Chaos)
}

// TestMachinePlatformStandby checks the platform subsection decodes as a
// presence-aware value: absent block, absent standby block, and explicit standby
// subsections are all distinguishable.
func TestMachinePlatformStandby(t *testing.T) {
	machine := func(t *testing.T, body string) blueprint.Machine {
		t.Helper()
		p := decodeHCL(t, `project "p" {
		  environment = "production"
		  features {}
		  site "north" {
		    machine "m1" {
		      role     = "node"
		      ip       = "10.0.1.10"
		      services = ["core-services"]
		      `+body+`
		    }
		  }
		}`)
		return p.Sites[0].Machines[0]
	}

	t.Run("no platform block", func(t *testing.T) {
		require.Nil(t, machine(t, "").Platform)
	})
	t.Run("standby enabled", func(t *testing.T) {
		m := machine(t, `platform {
		  nats {
		    client_port  = 4222
		    cluster_port = 6222
		  }
		  standby {
		    disabled = false
		  }
		}`)
		require.NotNil(t, m.Platform)
		require.NotNil(t, m.Platform.Nats)
		require.Equal(t, 4222, m.Platform.Nats.ClientPort)
		require.Equal(t, 6222, m.Platform.Nats.ClusterPort)
		require.NotNil(t, m.Platform.Standby)
		require.False(t, m.Platform.Standby.Disabled)
	})
	t.Run("standby explicitly disabled", func(t *testing.T) {
		m := machine(t, `platform {
		  nats {
		    client_port  = 4222
		    cluster_port = 6222
		  }
		  standby {
		    disabled = true
		  }
		}`)
		require.NotNil(t, m.Platform.Standby)
		require.True(t, m.Platform.Standby.Disabled)
	})
}

// TestMachinePlatformDecodeFailures checks the mandatory parts of the platform
// subsection are enforced by the decoder itself, before any validation runs.
//
// The disabled attribute matters most here. It is a bool, so an omitted one
// would decode to false and enable redundancy nobody asked for. Requiring it at
// decode time is what makes that impossible to express rather than merely
// invalid.
func TestMachinePlatformDecodeFailures(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		errText string
	}{
		{
			name: "standby block without disabled attribute",
			body: `platform {
			  nats {
			    client_port  = 4222
			    cluster_port = 6222
			  }
			  standby {}
			}`,
			errText: `"disabled"`,
		},
		{
			name: "nats block without client_port",
			body: `platform {
			  nats {
			    cluster_port = 6222
			  }
			  standby {
			    disabled = true
			  }
			}`,
			errText: `"client_port"`,
		},
		{
			name: "nats block without cluster_port",
			body: `platform {
			  nats {
			    client_port = 4222
			  }
			  standby {
			    disabled = true
			  }
			}`,
			errText: `"cluster_port"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := `project "p" {
			  environment = "production"
			  features {}
			  site "north" {
			    machine "m1" {
			      role     = "node"
			      ip       = "10.0.1.10"
			      services = ["core-services"]
			      ` + tc.body + `
			    }
			  }
			}`
			parser := hclparse.NewParser()
			file, diags := parser.ParseHCL([]byte(src), "test.hcl")
			require.False(t, diags.HasErrors(), diags.Error())

			var tf topologyFile
			diags = gohcl.DecodeBody(file.Body, nil, &tf)
			require.True(t, diags.HasErrors(), "expected a decode error")
			require.Contains(t, diags.Error(), tc.errText)
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
			        nats {
			          client_port  = 4222
			          cluster_port = 6222
			        }
			        standby {
			          disabled = false
			        }
			      }
			    }
			  }
			  site "south" {
			    machine "node-1" {
			      role     = "node"
			      ip       = "10.0.1.11"
			      services = ["core-services"]
			      platform {
			        nats {
			          client_port  = 4222
			          cluster_port = 6222
			        }
			        standby {
			          disabled = false
			        }
			      }
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

func TestProjectValidateNatsFailures(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*blueprint.Project)
		errText string
	}{
		{"missing platform block", func(p *blueprint.Project) { p.Sites[0].Machines[0].Platform = nil }, `machine "sensor": platform block is required`},
		{"missing nats block", func(p *blueprint.Project) { p.Sites[0].Machines[0].Platform.Nats = nil }, `machine "sensor": platform.nats block is required`},
		{"missing standby block", func(p *blueprint.Project) { p.Sites[0].Machines[0].Platform.Standby = nil }, `machine "sensor": platform.standby block is required`},
		{"zero client port", func(p *blueprint.Project) { p.Sites[0].Machines[0].Platform.Nats.ClientPort = 0 }, `platform.nats.client_port must be in range 1-65535, got 0`},
		{"negative client port", func(p *blueprint.Project) { p.Sites[0].Machines[0].Platform.Nats.ClientPort = -1 }, `platform.nats.client_port must be in range 1-65535, got -1`},
		{"client port above range", func(p *blueprint.Project) { p.Sites[0].Machines[0].Platform.Nats.ClientPort = 65536 }, `platform.nats.client_port must be in range 1-65535, got 65536`},
		{"zero cluster port", func(p *blueprint.Project) { p.Sites[0].Machines[0].Platform.Nats.ClusterPort = 0 }, `platform.nats.cluster_port must be in range 1-65535, got 0`},
		{"cluster port above range", func(p *blueprint.Project) { p.Sites[0].Machines[0].Platform.Nats.ClusterPort = 70000 }, `platform.nats.cluster_port must be in range 1-65535, got 70000`},
		{"colliding ports", func(p *blueprint.Project) {
			p.Sites[0].Machines[0].Platform.Nats.ClusterPort = p.Sites[0].Machines[0].Platform.Nats.ClientPort
		}, `platform.nats.client_port and cluster_port must differ, both are 4222`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := validProject()
			tc.mutate(p)
			require.ErrorContains(t, p.Validate(), tc.errText)
		})
	}
}

// TestMachineFenceNamespaceDefaults checks a machine that authors no fence block
// still resolves a namespace. Unlike the standby decision, defaulting here is safe:
// the machine's identity is hashed into the object name regardless, so a default
// cannot make two machines share a fence.
func TestMachineFenceNamespaceDefaults(t *testing.T) {
	p := validProject()
	machine := p.Sites[0].Machines[0]
	require.Nil(t, machine.Platform.Fence)
	require.Equal(t, blueprint.DefaultFenceNamespace, machine.FenceNamespace())
	require.NoError(t, p.Validate())
}

func TestMachineFenceNamespaceIsAuthored(t *testing.T) {
	p := validProject()
	p.Sites[0].Machines[0].Platform.Fence = &blueprint.Fence{Namespace: "rig-b"}
	require.NoError(t, p.Validate())
	require.Equal(t, "rig-b", p.Sites[0].Machines[0].FenceNamespace())
}

// TestProjectValidateFenceFailures checks an authored namespace that cannot be
// used in a kernel object name is refused at build time rather than at startup.
func TestProjectValidateFenceFailures(t *testing.T) {
	tests := map[string]struct {
		namespace string
		errText   string
	}{
		"blank":             {namespace: "   ", errText: "must not be blank"},
		"padded":            {namespace: " rig ", errText: "leading or trailing whitespace"},
		"backslash":         {namespace: `rig\b`, errText: "slash or backslash"},
		"forward slash":     {namespace: "rig/b", errText: "slash or backslash"},
		"longer than limit": {namespace: string(make([]byte, 65)), errText: "longer than"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			p := validProject()
			p.Sites[0].Machines[0].Platform.Fence = &blueprint.Fence{Namespace: test.namespace}
			require.ErrorContains(t, p.Validate(), test.errText)
		})
	}
}
