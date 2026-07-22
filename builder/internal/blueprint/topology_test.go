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
		// Two machines with a standby on one of them is the smallest site the
		// platform supports, so it is what an otherwise-valid fixture has to be.
		// A one-machine site is now rejected before any rule these tests are
		// about gets a chance to run.
		Sites: []blueprint.Site{{
			Name:     "north",
			Machines: []blueprint.Machine{validMachine(), namedMachine("relay", "10.0.1.11")},
		}},
	}
}

// validMachine is a machine that deploys both instances, with every listener on
// its own port. Tests that exercise one rule start from it and break only that
// rule, so a fixture missing an unrelated block cannot be what a failure is
// really reporting.
func validMachine() blueprint.Machine {
	return blueprint.Machine{
		Name:           "sensor",
		MachineProfile: "sensor-node",
		IP:             "10.0.1.10",
		Services:       []string{"sensor-services"},
		Platform: &blueprint.Platform{
			RuntimeDir: "C:/ProgramData/opdl/sensor/primary/runtime",
			DataDir:    "D:/opdl/sensor/primary",
			API:        &blueprint.API{LocalPort: 8080},
			WinService: &blueprint.WinService{Name: "primary"},
			EventStorage: &blueprint.EventStorage{
				EventFabric: &blueprint.EventFabric{
					Nats: &blueprint.Nats{
						ClientPort:        4222,
						ClusterPort:       6222,
						JetStreamStoreDir: "D:/opdl/sensor/primary/eventfabric/nats",
					},
				},
			},
			Standby: &blueprint.Standby{
				Disabled:   false,
				RuntimeDir: "C:/ProgramData/opdl/sensor/standby/runtime",
				DataDir:    "D:/opdl/sensor/standby",
				Lock:       &blueprint.Lock{WindowsMutex: "Global\\opdl-customer-a-north-sensor"},
				API:        &blueprint.API{LocalPort: 8081},
				WinService: &blueprint.WinService{Name: "standby"},
				EventStorage: &blueprint.EventStorage{
					EventFabric: &blueprint.EventFabric{
						Nats: &blueprint.Nats{
							ClientPort:        4322,
							ClusterPort:       6322,
							JetStreamStoreDir: "D:/opdl/sensor/standby/eventfabric/nats",
						},
					},
				},
			},
		},
	}
}

// namedMachine is validMachine under another name and address, for the rules
// that need a second machine in the project.
func namedMachine(name, ip string) blueprint.Machine {
	m := validMachine()
	m.Name = name
	m.IP = ip
	m.Platform.WinService = &blueprint.WinService{Name: name + "-primary"}
	// Everything a machine cannot share with another machine of its site is given
	// its own value here: the ownership object, and both per-instance directories.
	// Sharing a lock across machines is rejected, and sharing a directory is only
	// safe because two machines are two hosts, which a fixture on one host is not.
	m.Platform.RuntimeDir = "C:/ProgramData/opdl/" + name + "/primary/runtime"
	m.Platform.DataDir = "D:/opdl/" + name + "/primary"
	m.Platform.EventStorage.EventFabric.Nats.JetStreamStoreDir = "D:/opdl/" + name + "/primary/eventfabric/nats"
	m.Platform.Standby.WinService = &blueprint.WinService{Name: name + "-standby"}
	m.Platform.Standby.RuntimeDir = "C:/ProgramData/opdl/" + name + "/standby/runtime"
	m.Platform.Standby.DataDir = "D:/opdl/" + name + "/standby"
	m.Platform.Standby.EventStorage.EventFabric.Nats.JetStreamStoreDir = "D:/opdl/" + name + "/standby/eventfabric/nats"
	m.Platform.Standby.Lock = &blueprint.Lock{WindowsMutex: `Global\opdl-customer-a-north-` + name}
	return m
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

func TestProjectValidateMissingProfile(t *testing.T) {
	p := validProject()
	p.Sites[0].Machines[0].MachineProfile = ""
	require.ErrorContains(t, p.Validate(), "profile is required")
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
		Name:     "north",
		Machines: []blueprint.Machine{namedMachine("other", "10.0.1.12")},
	})
	require.ErrorContains(t, p.Validate(), "duplicate site")
}

// TestProjectValidateDuplicateIP checks an IP identifies exactly one machine.
// The platform derives its fabric addresses from a machine's IP on fixed ports,
// so two machines sharing one would derive the same addresses.
func TestProjectValidateDuplicateIP(t *testing.T) {
	t.Run("within one site", func(t *testing.T) {
		p := validProject()
		p.Sites[0].Machines = append(p.Sites[0].Machines, namedMachine("gateway", "10.0.1.10"))
		require.ErrorContains(t, p.Validate(), `machines "sensor" and "gateway" share ip "10.0.1.10"`)
	})

	t.Run("across sites", func(t *testing.T) {
		p := validProject()
		p.Sites = append(p.Sites, blueprint.Site{
			Name: "south",
			Machines: []blueprint.Machine{
				namedMachine("south-node", "10.0.1.10"),
				namedMachine("south-relay", "10.0.1.13"),
			},
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
		      profile  = "node"
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
		  event_storage {
		    eventfabric {
		      nats {
		        client_port         = 4222
		        cluster_port        = 6222
		        jetstream_store_dir = "D:/opdl/m1/primary/eventfabric/nats"
		      }
		    }
		  }
		  standby {
		    disabled = false
		    runtime_dir = "C:/ProgramData/opdl/m1/standby"
		    data_dir = "D:/opdl/m1/standby"
		    event_storage {
		      eventfabric {
		        nats {
		          client_port         = 4322
		          cluster_port        = 6322
		          jetstream_store_dir = "D:/opdl/m1/standby/eventfabric/nats"
		        }
		      }
		    }
		  }
		}`)
		require.NotNil(t, m.Platform)
		nats := m.Nats(false)
		require.NotNil(t, nats)
		require.Equal(t, 4222, nats.ClientPort)
		require.Equal(t, 6222, nats.ClusterPort)
		require.NotNil(t, m.Platform.Standby)
		require.False(t, m.Platform.Standby.Disabled)
	})
	t.Run("standby explicitly disabled", func(t *testing.T) {
		m := machine(t, `platform {
		  event_storage {
		    eventfabric {
		      nats {
		        client_port         = 4222
		        cluster_port        = 6222
		        jetstream_store_dir = "D:/opdl/m1/primary/eventfabric/nats"
		      }
		    }
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
func TestMachinePlatformDecodeFailures(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		errText string
	}{
		{
			name: "standby block without disabled attribute",
			body: `platform {
			  event_storage {
			    eventfabric {
			      nats {
			        client_port         = 4222
			        cluster_port        = 6222
			        jetstream_store_dir = "D:/opdl/m1/primary/eventfabric/nats"
			      }
			    }
			  }
			  standby {}
			}`,
			errText: `"disabled"`,
		},
		{
			name: "nats block without client_port",
			body: `platform {
			  event_storage {
			    eventfabric {
			      nats {
			        cluster_port        = 6222
			        jetstream_store_dir = "D:/opdl/m1/primary/eventfabric/nats"
			      }
			    }
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
			  event_storage {
			    eventfabric {
			      nats {
			        client_port         = 4222
			        jetstream_store_dir = "D:/opdl/m1/primary/eventfabric/nats"
			      }
			    }
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
			      profile  = "node"
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
			name: "missing profile",
			hcl: `project "bad-profile" {
			  environment = "production"
			  features {}
			  site "north" {
			    machine "m1" {
			      profile  = ""
			      ip       = "10.0.1.10"
			      services = ["core-services"]
			    }
			  }
			}`,
			errText: "profile is required",
		},
		{
			name: "missing ip",
			hcl: `project "bad-ip" {
			  environment = "production"
			  features {}
			  site "north" {
			    machine "m1" {
			      profile  = "node"
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
			      profile  = "node"
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
			      profile  = "node"
			      ip       = "10.0.1.10"
			      services = ["core-services"]
			      platform {
			        runtime_dir = "C:/ProgramData/opdl/node-1/primary"
			        data_dir = "D:/opdl/node-1/primary"
			        api {
			          local_port = 8080
			        }
			        winservice {
			          name = "primary"
			        }
			        event_storage {
			          eventfabric {
			            nats {
			              client_port         = 4222
			              cluster_port        = 6222
			              jetstream_store_dir = "D:/opdl/node-1/primary/eventfabric/nats"
			            }
			          }
			        }
			        standby {
			          disabled = false
			          runtime_dir = "C:/ProgramData/opdl/node-1/standby"
			          data_dir = "D:/opdl/node-1/standby"
			          lock {
			            windows_mutex = "Global\\dup-north-node-1"
			          }
			          api {
			            local_port = 8081
			          }
			          winservice {
			            name = "standby"
			          }
			          event_storage {
			            eventfabric {
			              nats {
			                client_port         = 4322
			                cluster_port        = 6322
			                jetstream_store_dir = "D:/opdl/node-1/standby/eventfabric/nats"
			              }
			            }
			          }
			        }
			      }
			    }
			  }
			  site "south" {
			    machine "node-1" {
			      profile  = "node"
			      ip       = "10.0.1.11"
			      services = ["core-services"]
			      platform {
			        runtime_dir = "C:/ProgramData/opdl/node-1/primary"
			        data_dir = "D:/opdl/node-1/primary"
			        api {
			          local_port = 8080
			        }
			        winservice {
			          name = "primary"
			        }
			        event_storage {
			          eventfabric {
			            nats {
			              client_port         = 4222
			              cluster_port        = 6222
			              jetstream_store_dir = "D:/opdl/node-1/primary/eventfabric/nats"
			            }
			          }
			        }
			        standby {
			          disabled = false
			          runtime_dir = "C:/ProgramData/opdl/node-1/standby"
			          data_dir = "D:/opdl/node-1/standby"
			          lock {
			            windows_mutex = "Global\\dup-south-node-1"
			          }
			          api {
			            local_port = 8081
			          }
			          winservice {
			            name = "standby"
			          }
			          event_storage {
			            eventfabric {
			              nats {
			                client_port         = 4322
			                cluster_port        = 6322
			                jetstream_store_dir = "D:/opdl/node-1/standby/eventfabric/nats"
			              }
			            }
			          }
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

// disableStandby opts a machine out of its Standby Instance the way a blueprint
// does: the decision plus every block that instance would have authored. A
// machine that keeps one of them is rejected, which is its own test.
func disableStandby(p *blueprint.Project) {
	standby := p.Sites[0].Machines[0].Platform.Standby
	standby.Disabled = true
	standby.RuntimeDir = ""
	standby.DataDir = ""
	standby.Lock = nil
	standby.API = nil
	standby.WinService = nil
	standby.EventStorage = nil
}

// TestStandbyEndpointsAreRejectedWhenNotDeployed checks each block a Standby
// Instance can author is refused when the machine deploys no standby. Authoring
// an endpoint for an instance that never runs states a decision that can never
// take effect, and a reader could not tell it from one that does.
func TestStandbyEndpointsAreRejectedWhenNotDeployed(t *testing.T) {
	tests := map[string]func(*blueprint.Standby){
		"lock":        func(s *blueprint.Standby) { s.Lock = &blueprint.Lock{WindowsMutex: "Global\\opdl-standby"} },
		"runtime_dir": func(s *blueprint.Standby) { s.RuntimeDir = "C:/ProgramData/opdl/sensor/standby" },
		"api":         func(s *blueprint.Standby) { s.API = &blueprint.API{LocalPort: 8081} },
		"winservice":  func(s *blueprint.Standby) { s.WinService = &blueprint.WinService{Name: "standby"} },
		"event_storage": func(s *blueprint.Standby) {
			s.EventStorage = &blueprint.EventStorage{EventFabric: &blueprint.EventFabric{Nats: &blueprint.Nats{ClientPort: 4322, ClusterPort: 6322, JetStreamStoreDir: "D:/opdl/sensor/standby/eventfabric/nats"}}}
		},
	}
	for label, author := range tests {
		t.Run(label, func(t *testing.T) {
			p := validProject()
			disableStandby(p)
			author(p.Sites[0].Machines[0].Platform.Standby)
			require.ErrorContains(t, p.Validate(), "standby is disabled")
		})
	}
}

// TestRuntimeDirIsRequiredPerDeployedInstance checks each deployed instance
// states where it writes its status file. An instance without one has nowhere to
// record what it is doing, and unlike a missing port that is not a failure to
// bind.
func TestRuntimeDirIsRequiredPerDeployedInstance(t *testing.T) {
	tests := map[string]struct {
		clear func(*blueprint.Platform)
		want  string
	}{
		"primary": {
			clear: func(pl *blueprint.Platform) { pl.RuntimeDir = "" },
			want:  "platform.runtime_dir is required",
		},
		"standby": {
			clear: func(pl *blueprint.Platform) { pl.Standby.RuntimeDir = "" },
			want:  "platform.standby.runtime_dir is required",
		},
		"primary is only whitespace": {
			clear: func(pl *blueprint.Platform) { pl.RuntimeDir = "   " },
			want:  "platform.runtime_dir is required",
		},
	}
	for label, test := range tests {
		t.Run(label, func(t *testing.T) {
			p := validProject()
			test.clear(p.Sites[0].Machines[0].Platform)
			require.ErrorContains(t, p.Validate(), test.want)
		})
	}
}

// TestMachineInstancesMustNotShareARuntimeDir is the quiet half of the six-port
// mistake. Two instances given one directory both start and both bind, and the
// only symptom is that each keeps overwriting the other's status file.
func TestMachineInstancesMustNotShareARuntimeDir(t *testing.T) {
	p := validProject()
	platform := p.Sites[0].Machines[0].Platform
	platform.Standby.RuntimeDir = platform.RuntimeDir
	require.ErrorContains(t, p.Validate(), "cannot share a runtime directory")
}

// TestMachineListenersMustNotShareAPort is the mistake the six-port shape
// invites: the two instances run together on one host, so nothing makes any pair
// of their listeners mutually exclusive.
func TestMachineListenersMustNotSharePort(t *testing.T) {
	tests := map[string]func(*blueprint.Platform){
		"standby api copies primary api": func(pl *blueprint.Platform) { pl.Standby.API.LocalPort = pl.API.LocalPort },
		"standby nats copies primary nats": func(pl *blueprint.Platform) {
			pl.Standby.EventStorage.EventFabric.Nats.ClientPort = pl.EventStorage.EventFabric.Nats.ClientPort
		},
		"standby cluster copies primary": func(pl *blueprint.Platform) {
			pl.Standby.EventStorage.EventFabric.Nats.ClusterPort = pl.EventStorage.EventFabric.Nats.ClusterPort
		},
		"api collides with this instance nats": func(pl *blueprint.Platform) { pl.API.LocalPort = pl.EventStorage.EventFabric.Nats.ClientPort },
	}
	for label, collide := range tests {
		t.Run(label, func(t *testing.T) {
			p := validProject()
			collide(p.Sites[0].Machines[0].Platform)
			require.ErrorContains(t, p.Validate(), "needs its own port")
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
		{"missing event_storage block", func(p *blueprint.Project) { p.Sites[0].Machines[0].Platform.EventStorage = nil }, `machine "sensor": platform.event_storage block is required`},
		{"missing standby block", func(p *blueprint.Project) { p.Sites[0].Machines[0].Platform.Standby = nil }, `machine "sensor": platform.standby block is required`},
		{"missing standby lock when deployed", func(p *blueprint.Project) { p.Sites[0].Machines[0].Platform.Standby.Lock = nil }, `machine "sensor": platform.standby.lock block is required when the standby is deployed`},
		{"zero client port", func(p *blueprint.Project) {
			p.Sites[0].Machines[0].Platform.EventStorage.EventFabric.Nats.ClientPort = 0
		}, `platform.event_storage.eventfabric.nats.client_port must be in range 1-65535, got 0`},
		{"negative client port", func(p *blueprint.Project) {
			p.Sites[0].Machines[0].Platform.EventStorage.EventFabric.Nats.ClientPort = -1
		}, `platform.event_storage.eventfabric.nats.client_port must be in range 1-65535, got -1`},
		{"client port above range", func(p *blueprint.Project) {
			p.Sites[0].Machines[0].Platform.EventStorage.EventFabric.Nats.ClientPort = 65536
		}, `platform.event_storage.eventfabric.nats.client_port must be in range 1-65535, got 65536`},
		{"zero cluster port", func(p *blueprint.Project) {
			p.Sites[0].Machines[0].Platform.EventStorage.EventFabric.Nats.ClusterPort = 0
		}, `platform.event_storage.eventfabric.nats.cluster_port must be in range 1-65535, got 0`},
		{"cluster port above range", func(p *blueprint.Project) {
			p.Sites[0].Machines[0].Platform.EventStorage.EventFabric.Nats.ClusterPort = 70000
		}, `platform.event_storage.eventfabric.nats.cluster_port must be in range 1-65535, got 70000`},
		{"colliding ports", func(p *blueprint.Project) {
			p.Sites[0].Machines[0].Platform.EventStorage.EventFabric.Nats.ClusterPort = p.Sites[0].Machines[0].Platform.EventStorage.EventFabric.Nats.ClientPort
		}, `platform.event_storage.eventfabric.nats.client_port and platform.event_storage.eventfabric.nats.cluster_port are both 4222`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := validProject()
			tc.mutate(p)
			require.ErrorContains(t, p.Validate(), tc.errText)
		})
	}
}

// TestMachineLock checks Lock returns nil when standby is disabled or omitted,
// and returns the authored Lock when deployed.
func TestMachineLock(t *testing.T) {
	p := validProject()
	machine := p.Sites[0].Machines[0]
	require.Equal(t, &blueprint.Lock{WindowsMutex: "Global\\opdl-customer-a-north-sensor"}, machine.Lock())

	disableStandby(p)
	require.Nil(t, p.Sites[0].Machines[0].Lock())
}

// TestProjectValidateLockFailures checks an authored lock windows_mutex that cannot be
// used in a kernel object name is refused at build time rather than at startup.
func TestProjectValidateLockFailures(t *testing.T) {
	tests := map[string]struct {
		windowsMutex string
		errText      string
	}{
		"blank":             {windowsMutex: "   ", errText: "windows_mutex is required"},
		"padded":            {windowsMutex: " Global\\opdl ", errText: "leading or trailing whitespace"},
		"no prefix":         {windowsMutex: "opdl-mutex", errText: `must start with Global\`},
		"backslash":         {windowsMutex: `Global\opdl\b`, errText: "slashes or backslashes after Global\\"},
		"forward slash":     {windowsMutex: "Global\\opdl/b", errText: "slashes or backslashes after Global\\"},
		"longer than limit": {windowsMutex: "Global\\" + string(make([]byte, 261)), errText: "longer than"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			p := validProject()
			p.Sites[0].Machines[0].Platform.Standby.Lock = &blueprint.Lock{WindowsMutex: test.windowsMutex}
			require.ErrorContains(t, p.Validate(), test.errText)
		})
	}
}

// TestMachineWinServiceIsRequiredForEveryDeployedInstance checks each deployed
// instance names its Windows Service, and only a deployed instance does.
func TestMachineWinServiceIsRequiredForEveryDeployedInstance(t *testing.T) {
	t.Run("primary block is required", func(t *testing.T) {
		p := validProject()
		p.Sites[0].Machines[0].Platform.WinService = nil
		require.ErrorContains(t, p.Validate(), "platform.winservice block is required")
	})

	t.Run("standby block is required when deployed", func(t *testing.T) {
		p := validProject()
		p.Sites[0].Machines[0].Platform.Standby.WinService = nil
		require.ErrorContains(t, p.Validate(), "platform.standby.winservice block is required")
	})

	// Naming a service for an instance the machine does not run states a decision
	// that can never take effect, and a reader could not tell it from one that does.
	t.Run("standby block is rejected when not deployed", func(t *testing.T) {
		p := validProject()
		p.Sites[0].Machines[0].Platform.Standby.Disabled = true
		require.ErrorContains(t, p.Validate(), "standby is disabled")
	})

	t.Run("disabled standby needs no service", func(t *testing.T) {
		p := validProject()
		disableStandby(p)
		require.NoError(t, p.Validate())
	})
}

// TestMachineWinServiceNamesMustDiffer is the one service-name collision Windows
// cannot refuse for us: the two instances share a host.
func TestMachineWinServiceNamesMustDiffer(t *testing.T) {
	p := validProject()
	shared := p.Sites[0].Machines[0].Platform.WinService.Name
	p.Sites[0].Machines[0].Platform.Standby.WinService.Name = shared
	require.ErrorContains(t, p.Validate(), "must differ")
}

func TestProjectValidateWinServiceFailures(t *testing.T) {
	tests := map[string]struct {
		name    string
		errText string
	}{
		"blank":     {name: "   ", errText: "name is required"},
		"padded":    {name: " svc ", errText: "leading or trailing whitespace"},
		"backslash": {name: `svc\a`, errText: "slash or backslash"},
		"slash":     {name: "svc/a", errText: "slash or backslash"},
		"too long":  {name: string(make([]byte, 257)), errText: "longer than"},
	}
	for label, test := range tests {
		t.Run(label, func(t *testing.T) {
			p := validProject()
			p.Sites[0].Machines[0].Platform.WinService.Name = test.name
			require.ErrorContains(t, p.Validate(), test.errText)
		})
	}
}

// TestWinServiceIdentityFillsDisplayNameDefault checks the builder resolves a
// complete identity rather than leaving a consumer to invent a display name.
func TestWinServiceIdentityFillsDisplayNameDefault(t *testing.T) {
	p := validProject()
	machine := p.Sites[0].Machines[0]
	machine.Platform.WinService.DisplayName = ""

	primary := machine.WinServiceIdentity(false)
	require.NotNil(t, primary)
	require.Equal(t, primary.Name, primary.DisplayName)

	machine.Platform.Standby.Disabled = true
	machine.Platform.Standby.API = nil
	machine.Platform.Standby.EventStorage = nil
	machine.Platform.Standby.WinService = nil
	require.Nil(t, machine.WinServiceIdentity(true), "an undeployed instance has no service")
}
