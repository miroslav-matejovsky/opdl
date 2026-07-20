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
					Nats: &blueprint.Nats{
						ClientAddress:  "10.0.1.10:4222",
						ClusterAddress: "10.0.1.10:6222",
						MonitorAddress: "127.0.0.1:8222",
					},
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
				Nats: &blueprint.Nats{
					ClientAddress:  "10.0.1.12:4222",
					ClusterAddress: "10.0.1.12:6222",
					MonitorAddress: "127.0.0.1:8222",
				},
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
				Nats: &blueprint.Nats{
					ClientAddress:  "10.0.1.10:4222",
					ClusterAddress: "10.0.1.10:6222",
					MonitorAddress: "127.0.0.1:8222",
				},
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
					Nats: &blueprint.Nats{
						ClientAddress:  "10.0.1.10:4222",
						ClusterAddress: "10.0.1.10:6222",
						MonitorAddress: "127.0.0.1:8222",
					},
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
	t.Run("platform block without standby", func(t *testing.T) {
		m := machine(t, `platform {
		  nats {
		    client_address  = "10.0.1.10:4222"
		    cluster_address = "10.0.1.10:6222"
		    monitor_address = "127.0.0.1:8222"
		  }
		}`)
		require.NotNil(t, m.Platform)
		require.NotNil(t, m.Platform.Nats)
		require.Nil(t, m.Platform.Standby)
	})
	t.Run("explicit standby", func(t *testing.T) {
		m := machine(t, `platform {
		  nats {
		    client_address  = "10.0.1.10:4222"
		    cluster_address = "10.0.1.10:6222"
		    monitor_address = "127.0.0.1:8222"
		  }
		  standby {
		    nats {
		      client_address  = "10.0.1.10:4223"
		      cluster_address = "10.0.1.10:6223"
		      monitor_address = "127.0.0.1:8223"
		    }
		  }
		}`)
		require.NotNil(t, m.Platform)
		require.NotNil(t, m.Platform.Nats)
		require.NotNil(t, m.Platform.Standby)
		require.NotNil(t, m.Platform.Standby.Nats)
		require.Equal(t, "10.0.1.10:4223", m.Platform.Standby.Nats.ClientAddress)
	})
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
			          client_address  = "10.0.1.10:4222"
			          cluster_address = "10.0.1.10:6222"
			          monitor_address = "127.0.0.1:8222"
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
			          client_address  = "10.0.1.11:4222"
			          cluster_address = "10.0.1.11:6222"
			          monitor_address = "127.0.0.1:8222"
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
		{"missing platform block", func(p *blueprint.Project) { p.Sites[0].Machines[0].Platform = nil }, "platform.nats configuration is required"},
		{"missing nats block", func(p *blueprint.Project) { p.Sites[0].Machines[0].Platform.Nats = nil }, "platform.nats configuration is required"},
		{"missing client_address", func(p *blueprint.Project) { p.Sites[0].Machines[0].Platform.Nats.ClientAddress = "" }, "platform.nats.client_address is required"},
		{"invalid client_address", func(p *blueprint.Project) { p.Sites[0].Machines[0].Platform.Nats.ClientAddress = "bad" }, "must be host:port"},
		{"missing cluster_address", func(p *blueprint.Project) { p.Sites[0].Machines[0].Platform.Nats.ClusterAddress = "" }, "platform.nats.cluster_address is required"},
		{"missing monitor_address", func(p *blueprint.Project) { p.Sites[0].Machines[0].Platform.Nats.MonitorAddress = "" }, "platform.nats.monitor_address is required"},
		{"duplicate nats address", func(p *blueprint.Project) {
			p.Sites[0].Machines[0].Platform.Nats.ClusterAddress = p.Sites[0].Machines[0].Platform.Nats.ClientAddress
		}, "is used more than once"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := validProject()
			tc.mutate(p)
			require.ErrorContains(t, p.Validate(), tc.errText)
		})
	}
}
