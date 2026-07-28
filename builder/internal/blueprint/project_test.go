package blueprint_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/builder/internal/blueprint"
)

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
	p.Sites[0].Machines[0].Services = []blueprint.Service{
		validService("sensor-services", 9101),
		validService("sensor-services", 9102),
	}
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
			      profile = ""
			      ip      = "10.0.1.10"
			      service "core-services" {
			        role = "master"
			        health_check {
			          type     = "http"
			          port     = 9101
			          path     = "/health"
			          interval = "10s"
			          timeout  = "2s"
			          retries  = 3
			        }
			      }
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
			      profile = "node"
			      ip      = ""
			      service "core-services" {
			        role = "master"
			        health_check {
			          type     = "http"
			          port     = 9101
			          path     = "/health"
			          interval = "10s"
			          timeout  = "2s"
			          retries  = 3
			        }
			      }
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
			      profile = "node"
			      ip      = "not-an-ip"
			      service "core-services" {
			        role = "master"
			        health_check {
			          type     = "http"
			          port     = 9101
			          path     = "/health"
			          interval = "10s"
			          timeout  = "2s"
			          retries  = 3
			        }
			      }
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
			      profile         = "node"
			      ip              = "10.0.1.10"
			      eventstore_file = "D:/opdl/node-1/machine-events.jsonl"
			      service "core-services" {
			        role = "master"
			        health_check {
			          type     = "http"
			          port     = 9101
			          path     = "/health"
			          interval = "10s"
			          timeout  = "2s"
			          retries  = 3
			        }
			      }
			      primary {
			        eventlog_file = "D:/opdl/node-1/primary/events.jsonl"
			        state_file    = "D:/opdl/node-1/primary/state.json"
			        log_file      = "D:/opdl/node-1/primary/platform.log"
			        api {
			          local_port          = 8080
			          read_header_timeout = "5s"
			          shutdown_timeout    = "10s"
			        }
			        winservice {
			          name = "primary"
			        }
			      }
			      standby {
			        disabled      = false
			        eventlog_file = "D:/opdl/node-1/standby/events.jsonl"
			        state_file    = "D:/opdl/node-1/standby/state.json"
			        log_file      = "D:/opdl/node-1/standby/platform.log"
			        lease {
			          file                   = "D:/opdl/node-1/lease"
			          duration               = "15s"
			          renewal_interval       = "5s"
			          health_check_interval  = "2s"
			          failback_stabilization = "30s"
			          lag_bound              = "30s"
			        }
			        api {
			          local_port          = 8081
			          read_header_timeout = "5s"
			          shutdown_timeout    = "10s"
			        }
			        winservice {
			          name = "standby"
			        }
			      }
			    }
			  }
			  site "south" {
			    machine "node-1" {
			      profile         = "node"
			      ip              = "10.0.1.11"
			      eventstore_file = "D:/opdl/node-1/machine-events.jsonl"
			      service "core-services" {
			        role = "master"
			        health_check {
			          type     = "http"
			          port     = 9101
			          path     = "/health"
			          interval = "10s"
			          timeout  = "2s"
			          retries  = 3
			        }
			      }
			      primary {
			        eventlog_file = "D:/opdl/node-1/primary/events.jsonl"
			        state_file    = "D:/opdl/node-1/primary/state.json"
			        log_file      = "D:/opdl/node-1/primary/platform.log"
			        api {
			          local_port          = 8080
			          read_header_timeout = "5s"
			          shutdown_timeout    = "10s"
			        }
			        winservice {
			          name = "primary"
			        }
			      }
			      standby {
			        disabled      = false
			        eventlog_file = "D:/opdl/node-1/standby/events.jsonl"
			        state_file    = "D:/opdl/node-1/standby/state.json"
			        log_file      = "D:/opdl/node-1/standby/platform.log"
			        lease {
			          file                   = "D:/opdl/node-1/lease"
			          duration               = "15s"
			          renewal_interval       = "5s"
			          health_check_interval  = "2s"
			          failback_stabilization = "30s"
			          lag_bound              = "30s"
			        }
			        api {
			          local_port          = 8081
			          read_header_timeout = "5s"
			          shutdown_timeout    = "10s"
			        }
			        winservice {
			          name = "standby"
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
