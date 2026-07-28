package blueprint_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/builder/internal/blueprint"
)

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

	t.Run("no instance blocks", func(t *testing.T) {
		m := machine(t, "")
		require.Nil(t, m.Primary)
		require.Nil(t, m.Standby)
	})
	t.Run("standby enabled", func(t *testing.T) {
		m := machine(t, `primary {
		    eventlog_file = "D:/opdl/m1/primary/events.jsonl"
		    state_file    = "D:/opdl/m1/primary/state.json"
		    api {
		      local_port          = 8080
		      read_header_timeout = "5s"
		      shutdown_timeout    = "10s"
		    }
		    winservice { name = "m1-primary" }
		  }
		  standby {
		    disabled      = false
		    eventlog_file = "D:/opdl/m1/standby/events.jsonl"
		    state_file    = "D:/opdl/m1/standby/state.json"
		  }`)
		require.NotNil(t, m.Primary)
		require.NotNil(t, m.Standby)
		require.False(t, m.Standby.Disabled)
	})
	t.Run("standby explicitly disabled", func(t *testing.T) {
		m := machine(t, `standby {
		    disabled = true
		  }`)
		require.NotNil(t, m.Standby)
		require.True(t, m.Standby.Disabled)
	})
}

func TestMachinePlatformDecodeFailures(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		errText string
	}{
		{
			name:    "standby block without disabled attribute",
			body:    `standby {}`,
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

func TestStandbyEndpointsAreRejectedWhenNotDeployed(t *testing.T) {
	tests := map[string]func(*blueprint.Standby){
		"lease":    func(s *blueprint.Standby) { s.Lease = validLease("D:/opdl/sensor/lease") },
		"log_file": func(s *blueprint.Standby) { s.LogFile = "D:/opdl/sensor/standby/platform.log" },
		"api": func(s *blueprint.Standby) {
			s.API = &blueprint.API{LocalPort: 8081, ReadHeaderTimeout: "5s", ShutdownTimeout: "10s"}
		},
		"winservice": func(s *blueprint.Standby) { s.WinService = &blueprint.WinService{Name: "standby"} },
	}
	for label, author := range tests {
		t.Run(label, func(t *testing.T) {
			p := validProject()
			disableStandby(p)
			author(p.Sites[0].Machines[0].Standby)
			require.ErrorContains(t, p.Validate(), "standby is disabled")
		})
	}
}

// TestInstanceFilesAreRequired checks every deployed instance states each of the
// three local files it owns. None has a usable default: an omitted path would
// resolve as the empty string, which is not a file the runtime could open.
func TestInstanceFilesAreRequired(t *testing.T) {
	tests := map[string]func(*blueprint.Machine){
		"primary.eventlog_file": func(m *blueprint.Machine) { m.Primary.EventlogFile = "" },
		"primary.state_file":    func(m *blueprint.Machine) { m.Primary.StateFile = "" },
		"primary.log_file":      func(m *blueprint.Machine) { m.Primary.LogFile = "" },
		"standby.eventlog_file": func(m *blueprint.Machine) { m.Standby.EventlogFile = "" },
		"standby.state_file":    func(m *blueprint.Machine) { m.Standby.StateFile = "" },
		"standby.log_file":      func(m *blueprint.Machine) { m.Standby.LogFile = "" },
	}
	for where, blank := range tests {
		t.Run("missing "+where, func(t *testing.T) {
			p := validProject()
			blank(&p.Sites[0].Machines[0])
			require.ErrorContains(t, p.Validate(), where+" is required")
		})
	}
}

// TestInstanceFilesMustNotCollide checks no two of a machine's local files name
// one path.
//
// A repeat within one instance and a repeat across the two are the same failure.
// Both instances run together on one host and each writes every file it owns, so
// either way the machine has two writers on one file.
func TestInstanceFilesMustNotCollide(t *testing.T) {
	tests := map[string]struct {
		collide func(*blueprint.Machine)
		errText string
	}{
		"an instance logs into its own event record": {
			func(m *blueprint.Machine) { m.Primary.LogFile = m.Primary.EventlogFile },
			"primary.eventlog_file and primary.log_file are both",
		},
		"the two instances share a log": {
			func(m *blueprint.Machine) { m.Standby.LogFile = m.Primary.LogFile },
			"primary.log_file and standby.log_file are both",
		},
		"the two instances share a log spelled differently": {
			func(m *blueprint.Machine) { m.Standby.LogFile = `D:\OPDL\SENSOR\primary\PLATFORM.LOG` },
			"primary.log_file and standby.log_file are both",
		},
		"the machine's store is an instance's log": {
			func(m *blueprint.Machine) { m.EventstoreFile = m.Standby.LogFile },
			"eventstore_file and standby.log_file are both",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			p := validProject()
			test.collide(&p.Sites[0].Machines[0])
			require.ErrorContains(t, p.Validate(), test.errText)
		})
	}
}

// TestMachineFilesCarryTheAuthoredPaths checks each instance's files are read
// off that instance's own block, and that an instance the machine does not
// deploy has none.
func TestMachineFilesCarryTheAuthoredPaths(t *testing.T) {
	p := validProject()
	machine := p.Sites[0].Machines[0]

	require.Equal(t, blueprint.InstanceFiles{
		EventlogFile: "D:/opdl/sensor/primary/events.jsonl",
		StateFile:    "D:/opdl/sensor/primary/state.json",
		LogFile:      "D:/opdl/sensor/primary/platform.log",
	}, machine.Files(false))
	require.Equal(t, blueprint.InstanceFiles{
		EventlogFile: "D:/opdl/sensor/standby/events.jsonl",
		StateFile:    "D:/opdl/sensor/standby/state.json",
		LogFile:      "D:/opdl/sensor/standby/platform.log",
	}, machine.Files(true))

	disableStandby(p)
	require.Equal(t, blueprint.InstanceFiles{}, p.Sites[0].Machines[0].Files(true))
}

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
	for name, test := range tests {
		for _, standby := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/standby=%t", name, standby), func(t *testing.T) {
				p := validProject()
				machine := &p.Sites[0].Machines[0]
				api := machine.Primary.API
				if standby {
					api = machine.Standby.API
				}
				test.mutate(api)
				require.ErrorContains(t, p.Validate(), test.errText)
			})
		}
	}
}

func TestMachineEndpointsCarryTheAPITimeouts(t *testing.T) {
	p := validProject()
	endpoints := p.Sites[0].Machines[0].Endpoints(false)
	require.Equal(t, "5s", endpoints.APIReadHeaderTimeout)
	require.Equal(t, "10s", endpoints.APIShutdownTimeout)
}

func TestMachineLease(t *testing.T) {
	p := validProject()
	machine := p.Sites[0].Machines[0]
	require.Equal(t, validLease("D:/opdl/sensor/lease"), machine.Lease())

	disableStandby(p)
	require.Nil(t, p.Sites[0].Machines[0].Lease())
}

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
			p.Sites[0].Machines[0].Standby.Lease = lease
			require.ErrorContains(t, p.Validate(), test.errText)
		})
	}
}

func TestMachineWinServiceIsRequiredForEveryDeployedInstance(t *testing.T) {
	t.Run("primary block is required", func(t *testing.T) {
		p := validProject()
		p.Sites[0].Machines[0].Primary.WinService = nil
		require.ErrorContains(t, p.Validate(), "primary.winservice block is required")
	})

	t.Run("standby block is required when deployed", func(t *testing.T) {
		p := validProject()
		p.Sites[0].Machines[0].Standby.WinService = nil
		require.ErrorContains(t, p.Validate(), "standby.winservice block is required")
	})

	t.Run("standby block is rejected when not deployed", func(t *testing.T) {
		p := validProject()
		p.Sites[0].Machines[0].Standby.Disabled = true
		require.ErrorContains(t, p.Validate(), "standby is disabled")
	})

	t.Run("disabled standby needs no service", func(t *testing.T) {
		p := validProject()
		disableStandby(p)
		require.NoError(t, p.Validate())
	})
}

func TestMachineWinServiceNamesMustDiffer(t *testing.T) {
	p := validProject()
	shared := p.Sites[0].Machines[0].Primary.WinService.Name
	p.Sites[0].Machines[0].Standby.WinService.Name = shared
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
			p.Sites[0].Machines[0].Primary.WinService.Name = test.name
			require.ErrorContains(t, p.Validate(), test.errText)
		})
	}
}

func TestWinServiceIdentityFillsDisplayNameDefault(t *testing.T) {
	p := validProject()
	machine := p.Sites[0].Machines[0]
	machine.Primary.WinService.DisplayName = ""

	primary := machine.WinServiceIdentity(false)
	require.NotNil(t, primary)
	require.Equal(t, primary.Name, primary.DisplayName)

	machine.Standby.Disabled = true
	machine.Standby.API = nil
	machine.Standby.WinService = nil
	require.Nil(t, machine.WinServiceIdentity(true), "an undeployed instance has no service")
}
