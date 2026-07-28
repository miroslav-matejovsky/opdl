package blueprint_test

import (
	"testing"

	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/builder/internal/blueprint"
)

func TestServiceDecodesFromHCL(t *testing.T) {
	p := decodeHCL(t, `project "p" {
	  environment = "production"
	  site "north" {
	    machine "m1" {
	      profile = "node"
	      ip      = "10.0.1.10"

	      service "alarm-service" {
	        role = "master"

	        health_check {
	          type     = "http"
	          port     = 8080
	          path     = "/health"
	          interval = "10s"
	          timeout  = "2s"
	          retries  = 3
	        }
	      }

	      service "reporting-service" {
	        role = "slave"

	        health_check {
	          type     = "http"
	          port     = 8081
	          path     = "/health/ready"
	          interval = "30s"
	          timeout  = "5s"
	          retries  = 5
	        }
	      }
	    }
	  }
	}`)

	m := p.Sites[0].Machines[0]
	require.Equal(t, []string{"alarm-service", "reporting-service"}, m.ServiceNames())
	require.Equal(t, "master", m.Services[0].Role)
	require.Equal(t, "slave", m.Services[1].Role)
	require.Equal(t, blueprint.HealthCheck{
		Type:     "http",
		Port:     8080,
		Path:     "/health",
		Interval: "10s",
		Timeout:  "2s",
		Retries:  3,
	}, m.Services[0].HealthCheck)
	require.Equal(t, blueprint.HealthCheck{
		Type:     "http",
		Port:     8081,
		Path:     "/health/ready",
		Interval: "30s",
		Timeout:  "5s",
		Retries:  5,
	}, m.Services[1].HealthCheck, "each service is probed on its own terms")
}

// TestServiceHealthCheckIsRequired checks a service that states no probe is
// refused by the decoder, before validation runs. A service the platform cannot
// ask about is one nobody can be told has stopped.
func TestServiceHealthCheckIsRequired(t *testing.T) {
	src := `project "p" {
	  environment = "production"
	  site "north" {
	    machine "m1" {
	      profile = "node"
	      ip      = "10.0.1.10"

	      service "alarm-service" {
	        role = "master"
	      }
	    }
	  }
	}`
	parser := hclparse.NewParser()
	file, diags := parser.ParseHCL([]byte(src), "test.hcl")
	require.False(t, diags.HasErrors(), diags.Error())

	var tf topologyFile
	diags = gohcl.DecodeBody(file.Body, nil, &tf)
	require.True(t, diags.HasErrors(), "expected a decode error")
	require.Contains(t, diags.Error(), "health_check")
}

func TestServiceRole(t *testing.T) {
	t.Run("both roles are accepted", func(t *testing.T) {
		for _, role := range []string{"master", "slave"} {
			p := validProject()
			p.Sites[0].Machines[0].Services[0].Role = role
			require.NoError(t, p.Validate(), "role %q", role)
		}
	})

	failures := map[string]struct {
		role    string
		errText string
	}{
		"missing":     {"", `service "sensor-services": role is required`},
		"blank":       {"   ", "role is required"},
		"unknown":     {"primary", `role "primary" is not a known role; the supported roles are master, slave`},
		"wrong case":  {"Master", `role "Master" is not a known role`},
		"padded":      {" master ", `role " master " is not a known role`},
		"the machine": {"master-server", `role "master-server" is not a known role`},
	}
	for label, test := range failures {
		t.Run(label, func(t *testing.T) {
			p := validProject()
			p.Sites[0].Machines[0].Services[0].Role = test.role
			require.ErrorContains(t, p.Validate(), test.errText)
		})
	}
}

func TestServiceNameFailures(t *testing.T) {
	tests := map[string]struct {
		name    string
		errText string
	}{
		"empty":  {"", "service with empty name"},
		"blank":  {"   ", "service with empty name"},
		"padded": {" alarm-service ", "must not have leading or trailing whitespace"},
	}
	for label, test := range tests {
		t.Run(label, func(t *testing.T) {
			p := validProject()
			p.Sites[0].Machines[0].Services = []blueprint.Service{validService(test.name, 9101)}
			require.ErrorContains(t, p.Validate(), test.errText)
		})
	}
}

func TestServiceHealthCheckFailures(t *testing.T) {
	tests := map[string]struct {
		mutate  func(*blueprint.HealthCheck)
		errText string
	}{
		"no type": {
			func(c *blueprint.HealthCheck) { c.Type = "  " },
			"health_check.type is required",
		},
		"unknown type": {
			func(c *blueprint.HealthCheck) { c.Type = "ping" },
			`health_check.type "ping" is not a known probe; the supported types are http`,
		},
		"padded type": {
			func(c *blueprint.HealthCheck) { c.Type = " http " },
			`health_check.type " http " is not a known probe`,
		},
		"http without a path": {
			func(c *blueprint.HealthCheck) { c.Path = "" },
			"health_check.path is required for an http probe",
		},
		"padded path": {
			func(c *blueprint.HealthCheck) { c.Path = " /health " },
			"must not have leading or trailing whitespace",
		},
		"relative path": {
			func(c *blueprint.HealthCheck) { c.Path = "health" },
			`health_check.path "health" must start with "/"`,
		},
		"no port": {
			func(c *blueprint.HealthCheck) { c.Port = 0 },
			"health_check.port must be in range 1-65535",
		},
		"port out of range": {
			func(c *blueprint.HealthCheck) { c.Port = 70000 },
			"health_check.port must be in range 1-65535",
		},
		"no interval": {
			func(c *blueprint.HealthCheck) { c.Interval = "" },
			"health_check.interval is required",
		},
		"unparseable interval": {
			func(c *blueprint.HealthCheck) { c.Interval = "soon" },
			"is not a valid duration",
		},
		"negative interval": {
			func(c *blueprint.HealthCheck) { c.Interval = "-10s" },
			"health_check.interval -10s must be positive",
		},
		"no timeout": {
			func(c *blueprint.HealthCheck) { c.Timeout = "" },
			"health_check.timeout is required",
		},
		"timeout longer than the interval": {
			func(c *blueprint.HealthCheck) { c.Timeout = "30s" },
			`health_check.timeout 30s must be shorter than interval 10s`,
		},
		"timeout equal to the interval": {
			func(c *blueprint.HealthCheck) { c.Timeout = c.Interval },
			"must be shorter than interval",
		},
		"no retries": {
			func(c *blueprint.HealthCheck) { c.Retries = 0 },
			"health_check.retries must be at least 1, got 0",
		},
		"negative retries": {
			func(c *blueprint.HealthCheck) { c.Retries = -1 },
			"health_check.retries must be at least 1, got -1",
		},
	}
	for label, test := range tests {
		t.Run(label, func(t *testing.T) {
			p := validProject()
			service := validService("alarm-service", 9101)
			test.mutate(&service.HealthCheck)
			p.Sites[0].Machines[0].Services = []blueprint.Service{service}

			err := p.Validate()
			require.ErrorContains(t, err, test.errText)
			require.ErrorContains(t, err, `machine "sensor": service "alarm-service"`,
				"the error names the machine and the service an author has to fix")
		})
	}
}
