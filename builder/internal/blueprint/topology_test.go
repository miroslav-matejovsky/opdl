package blueprint_test

import (
	"fmt"
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
			MachineEventsFile: "D:/opdl/sensor/machine-events.jsonl",
			EventsFile:        "D:/opdl/sensor/primary/events.jsonl",
			StateFile:         "D:/opdl/sensor/primary/state.json",
			API:               &blueprint.API{LocalPort: 8080, ReadHeaderTimeout: "5s", ShutdownTimeout: "10s"},
			WinService:        &blueprint.WinService{Name: "primary"},
			Standby: &blueprint.Standby{
				EventsFile: "D:/opdl/sensor/standby/events.jsonl",
				StateFile:  "D:/opdl/sensor/standby/state.json",
				Lease:      validLease("D:/opdl/sensor/lease"),
				API:        &blueprint.API{LocalPort: 8081, ReadHeaderTimeout: "5s", ShutdownTimeout: "10s"},
				WinService: &blueprint.WinService{Name: "standby"},
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
	// its own value here: the ownership object, and every per-instance file.
	// Sharing a lock across machines is rejected, and sharing a file is only safe
	// because two machines are two hosts, which a fixture on one host is not.
	m.Platform.MachineEventsFile = "D:/opdl/" + name + "/machine-events.jsonl"
	m.Platform.EventsFile = "D:/opdl/" + name + "/primary/events.jsonl"
	m.Platform.StateFile = "D:/opdl/" + name + "/primary/state.json"
	m.Platform.Standby.WinService = &blueprint.WinService{Name: name + "-standby"}
	m.Platform.Standby.EventsFile = "D:/opdl/" + name + "/standby/events.jsonl"
	m.Platform.Standby.StateFile = "D:/opdl/" + name + "/standby/state.json"
	m.Platform.Standby.Lease = validLease("D:/opdl/" + name + "/lease")
	return m
}

// validLease is a usable Primary Ownership lease policy for a machine whose lease
// file lives at file. The two instances run on one host, so nothing about it is
// shared with another machine except that both machines derive their own file.
func validLease(file string) *blueprint.Lease {
	return &blueprint.Lease{
		File:                  file,
		Duration:              "15s",
		RenewalInterval:       "5s",
		HealthCheckInterval:   "2s",
		FailbackStabilization: "30s",
		LagBound:              "30s",
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

// TestMachinePlatformStandby checks the platform subsection decodes as a
// presence-aware value: absent block, absent standby block, and explicit standby
// subsections are all distinguishable.
func TestMachinePlatformStandby(t *testing.T) {
	machine := func(t *testing.T, body string) blueprint.Machine {
		t.Helper()
		p := decodeHCL(t, `project "p" {
		  environment = "production"
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
		  standby {
		    disabled = false
		    events_file = "D:/opdl/m1/standby/events.jsonl"
		    state_file  = "D:/opdl/m1/standby/state.json"
		  }
		}`)
		require.NotNil(t, m.Platform)
		require.NotNil(t, m.Platform.Standby)
		require.False(t, m.Platform.Standby.Disabled)
	})
	t.Run("standby explicitly disabled", func(t *testing.T) {
		m := machine(t, `platform {
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
			  standby {}
			}`,
			errText: `"disabled"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := `project "p" {
			  environment = "production"
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
			  site "north" {
			    machine "node-1" {
			      profile  = "node"
			      ip       = "10.0.1.10"
			      services = ["core-services"]
			      platform {
			        machine_events_file = "D:/opdl/node-1/machine-events.jsonl"
			        events_file = "D:/opdl/node-1/primary/events.jsonl"
			        state_file  = "D:/opdl/node-1/primary/state.json"
			        api {
			          local_port = 8080
			          read_header_timeout = "5s"
			          shutdown_timeout = "10s"
			        }
			        winservice {
			          name = "primary"
			        }
			        standby {
			          disabled = false
			          events_file = "D:/opdl/node-1/standby/events.jsonl"
			          state_file  = "D:/opdl/node-1/standby/state.json"
			          lease {
			            file                   = "D:/opdl/node-1/lease"
			            duration               = "15s"
			            renewal_interval       = "5s"
			            health_check_interval  = "2s"
			            failback_stabilization = "30s"
			            lag_bound              = "30s"
			          }
			          api {
			            local_port = 8081
			            read_header_timeout = "5s"
			            shutdown_timeout = "10s"
			          }
			          winservice {
			            name = "standby"
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
			        machine_events_file = "D:/opdl/node-1/machine-events.jsonl"
			        events_file = "D:/opdl/node-1/primary/events.jsonl"
			        state_file  = "D:/opdl/node-1/primary/state.json"
			        api {
			          local_port = 8080
			          read_header_timeout = "5s"
			          shutdown_timeout = "10s"
			        }
			        winservice {
			          name = "primary"
			        }
			        standby {
			          disabled = false
			          events_file = "D:/opdl/node-1/standby/events.jsonl"
			          state_file  = "D:/opdl/node-1/standby/state.json"
			          lease {
			            file                   = "D:/opdl/node-1/lease"
			            duration               = "15s"
			            renewal_interval       = "5s"
			            health_check_interval  = "2s"
			            failback_stabilization = "30s"
			            lag_bound              = "30s"
			          }
			          api {
			            local_port = 8081
			            read_header_timeout = "5s"
			            shutdown_timeout = "10s"
			          }
			          winservice {
			            name = "standby"
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
	standby.EventsFile = ""
	standby.StateFile = ""
	standby.Lease = nil
	standby.API = nil
	standby.WinService = nil
}

// TestStandbyEndpointsAreRejectedWhenNotDeployed checks each block a Standby
// Instance can author is refused when the machine deploys no standby. Authoring
// an endpoint for an instance that never runs states a decision that can never
// take effect, and a reader could not tell it from one that does.
func TestStandbyEndpointsAreRejectedWhenNotDeployed(t *testing.T) {
	tests := map[string]func(*blueprint.Standby){
		"lease": func(s *blueprint.Standby) { s.Lease = validLease("D:/opdl/sensor/lease") },
		"api": func(s *blueprint.Standby) {
			s.API = &blueprint.API{LocalPort: 8081, ReadHeaderTimeout: "5s", ShutdownTimeout: "10s"}
		},
		"winservice": func(s *blueprint.Standby) { s.WinService = &blueprint.WinService{Name: "standby"} },
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

// TestMachineListenersMustNotShareAPort is the mistake the six-port shape
// invites: the two instances run together on one host, so nothing makes any pair
// of their listeners mutually exclusive.
func TestMachineListenersMustNotSharePort(t *testing.T) {
	tests := map[string]func(*blueprint.Platform){
		"standby api copies primary api": func(pl *blueprint.Platform) { pl.Standby.API.LocalPort = pl.API.LocalPort },
	}
	for label, collide := range tests {
		t.Run(label, func(t *testing.T) {
			p := validProject()
			collide(p.Sites[0].Machines[0].Platform)
			require.ErrorContains(t, p.Validate(), "needs its own port")
		})
	}
}

// TestProjectValidateAPITimeoutFailures checks an authored listener timeout that
// cannot be used is refused at build time rather than at startup. A zero timeout
// is refused rather than read as "no limit": the two look identical in a
// blueprint and only one of them is ever meant.
func TestProjectValidateAPITimeoutFailures(t *testing.T) {
	tests := map[string]struct {
		mutate  func(*blueprint.API)
		errText string
	}{
		"blank read header timeout":        {func(a *blueprint.API) { a.ReadHeaderTimeout = "" }, "api.read_header_timeout is required"},
		"bad read header timeout":          {func(a *blueprint.API) { a.ReadHeaderTimeout = "soon" }, "is not a valid duration"},
		"non-positive read header timeout": {func(a *blueprint.API) { a.ReadHeaderTimeout = "0s" }, "must be positive"},
		"blank shutdown timeout":           {func(a *blueprint.API) { a.ShutdownTimeout = "" }, "api.shutdown_timeout is required"},
		"non-positive shutdown timeout":    {func(a *blueprint.API) { a.ShutdownTimeout = "-1s" }, "must be positive"},
	}
	// Both instances are checked: they author their own listeners, so a rule
	// applied to only one of them would let the other ship an unusable timeout.
	for name, test := range tests {
		for _, standby := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/standby=%t", name, standby), func(t *testing.T) {
				p := validProject()
				platform := p.Sites[0].Machines[0].Platform
				api := platform.API
				if standby {
					api = platform.Standby.API
				}
				test.mutate(api)
				require.ErrorContains(t, p.Validate(), test.errText)
			})
		}
	}
}

// TestMachineEndpointsCarryTheAPITimeouts checks the timeouts reach resolution
// alongside the port, so an instance's listener and its bounds come from one
// place.
func TestMachineEndpointsCarryTheAPITimeouts(t *testing.T) {
	p := validProject()
	endpoints := p.Sites[0].Machines[0].Endpoints(false)
	require.Equal(t, "5s", endpoints.APIReadHeaderTimeout)
	require.Equal(t, "10s", endpoints.APIShutdownTimeout)
}

// TestMachineLease checks Lease returns nil when standby is disabled or omitted,
// and returns the authored Lease when deployed.
func TestMachineLease(t *testing.T) {
	p := validProject()
	machine := p.Sites[0].Machines[0]
	require.Equal(t, validLease("D:/opdl/sensor/lease"), machine.Lease())

	disableStandby(p)
	require.Nil(t, p.Sites[0].Machines[0].Lease())
}

// TestMachineEventsFileIsTheMachines checks the machine's own store is read off
// the machine whether or not it deploys a standby. It is not the lease: a
// single-instance machine has machine facts too.
func TestMachineEventsFileIsTheMachines(t *testing.T) {
	p := validProject()
	require.Equal(t, "D:/opdl/sensor/machine-events.jsonl", p.Sites[0].Machines[0].MachineEventsFile())

	disableStandby(p)
	require.NoError(t, p.Validate())
	require.Equal(t, "D:/opdl/sensor/machine-events.jsonl", p.Sites[0].Machines[0].MachineEventsFile(),
		"a machine with one instance still keeps the machine's own account")
}

// TestProjectValidateMachineEventsFileFailures checks a machine store that
// cannot be used is refused at build time rather than at startup.
func TestProjectValidateMachineEventsFileFailures(t *testing.T) {
	tests := map[string]struct {
		mutate  func(*blueprint.Platform)
		errText string
	}{
		"blank": {
			func(p *blueprint.Platform) { p.MachineEventsFile = "   " },
			"platform.machine_events_file is required",
		},
		"padded": {
			func(p *blueprint.Platform) { p.MachineEventsFile = " D:/opdl/sensor/machine-events.jsonl " },
			"leading or trailing whitespace",
		},
		"the primary instance's record": {
			func(p *blueprint.Platform) { p.MachineEventsFile = p.EventsFile },
			"platform.machine_events_file and platform.events_file are both",
		},
		"the standby instance's record spelled differently": {
			func(p *blueprint.Platform) { p.MachineEventsFile = `D:\OPDL\SENSOR\standby\EVENTS.JSONL` },
			"platform.machine_events_file and platform.standby.events_file are both",
		},
		"the lease file": {
			func(p *blueprint.Platform) { p.MachineEventsFile = p.Standby.Lease.File },
			"platform.machine_events_file and platform.standby.lease.file are both",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			p := validProject()
			test.mutate(p.Sites[0].Machines[0].Platform)
			require.ErrorContains(t, p.Validate(), test.errText)
		})
	}
}

// TestProjectValidateLeaseFailures checks an authored lease that cannot be used
// is refused at build time rather than at startup.
func TestProjectValidateLeaseFailures(t *testing.T) {
	tests := map[string]struct {
		mutate  func(*blueprint.Lease)
		errText string
	}{
		"blank file":            {func(l *blueprint.Lease) { l.File = "   " }, "lease.file is required"},
		"padded file":           {func(l *blueprint.Lease) { l.File = " D:/opdl/lease " }, "leading or trailing whitespace"},
		"blank duration":        {func(l *blueprint.Lease) { l.Duration = "" }, "lease.duration is required"},
		"bad duration":          {func(l *blueprint.Lease) { l.Duration = "soon" }, "is not a valid duration"},
		"non-positive duration": {func(l *blueprint.Lease) { l.Duration = "0s" }, "must be positive"},
		"blank renewal":         {func(l *blueprint.Lease) { l.RenewalInterval = "" }, "lease.renewal_interval is required"},
		"renewal not shorter":   {func(l *blueprint.Lease) { l.RenewalInterval = "15s" }, "must be shorter than duration"},
		"blank health interval": {func(l *blueprint.Lease) { l.HealthCheckInterval = "" }, "lease.health_check_interval is required"},
		"blank failback":        {func(l *blueprint.Lease) { l.FailbackStabilization = "" }, "lease.failback_stabilization is required"},
		"blank lag bound":       {func(l *blueprint.Lease) { l.LagBound = "" }, "lease.lag_bound is required"},
		"bad lag bound":         {func(l *blueprint.Lease) { l.LagBound = "soon" }, "is not a valid duration"},
		"non-positive lag bound": {
			func(l *blueprint.Lease) { l.LagBound = "0s" }, "must be positive",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			p := validProject()
			lease := validLease("D:/opdl/sensor/lease")
			test.mutate(lease)
			p.Sites[0].Machines[0].Platform.Standby.Lease = lease
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
	machine.Platform.Standby.WinService = nil
	require.Nil(t, machine.WinServiceIdentity(true), "an undeployed instance has no service")
}
