package deployment

import (
	"fmt"
	"strings"
)

// Descriptor is the per-machine deployment artifact: the resolved execution
// definition the builder produces (as JSON) and the platform runs from. It
// names one machine's identity within its project, the features turned on
// for it, and the service groups it hosts.
//
// The json struct tags are the on-disk wire format: the builder marshals a
// Descriptor to JSON and embeds it; the platform unmarshals it back. No HCL
// is involved at runtime.
type Descriptor struct {
	// Environment is the target environment, e.g. "production".
	Environment string `json:"environment"`
	// Project is the project (customer) identifier.
	Project string `json:"project"`
	// Site is the site within the project this machine belongs to.
	Site string `json:"site"`
	// Machine is the machine identifier this binary is configured for.
	Machine string `json:"machine"`
	// Role is the machine's role, e.g. "sensor-node".
	Role string `json:"role"`
	// Features are the capability switches enabled for this machine.
	Features Features `json:"features"`
	// Services lists the service groups this machine hosts.
	Services []string `json:"services"`
}

// Features are the capability switches for a machine.
type Features struct {
	// Redundancy runs two instances of platform services in parallel on the
	// same machine, so one can fail without taking the service down.
	Redundancy bool `json:"redundancy"`
	// Chaos enables deliberately injecting failures to test the system's
	// resilience.
	Chaos bool `json:"chaos"`
}

// Validate checks that a deployment descriptor is internally coherent:
// identity fields are set and services are non-empty with no duplicates. It
// fails fast on the first problem so neither the build step nor the runtime
// proceeds on a broken deployment.
func (d Descriptor) Validate() error {
	if strings.TrimSpace(d.Environment) == "" {
		return fmt.Errorf("descriptor: environment is required")
	}
	if strings.TrimSpace(d.Project) == "" {
		return fmt.Errorf("descriptor: project is required")
	}
	if strings.TrimSpace(d.Site) == "" {
		return fmt.Errorf("descriptor: site is required")
	}
	if strings.TrimSpace(d.Machine) == "" {
		return fmt.Errorf("descriptor: machine is required")
	}
	if strings.TrimSpace(d.Role) == "" {
		return fmt.Errorf("descriptor: role is required")
	}
	if len(d.Services) == 0 {
		return fmt.Errorf("descriptor: at least one service is required")
	}

	seen := make(map[string]bool, len(d.Services))
	for _, name := range d.Services {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("descriptor: service with empty name")
		}
		if seen[name] {
			return fmt.Errorf("descriptor: service %q assigned more than once", name)
		}
		seen[name] = true
	}
	return nil
}

// Summary renders a compact, human-readable overview. Both the builder plan
// output and the platform startup log use it so the two sides describe a
// machine identically.
func (d Descriptor) Summary() string {
	var features []string
	if d.Features.Chaos {
		features = append(features, "chaos")
	}
	if d.Features.Redundancy {
		features = append(features, "redundancy")
	}

	lines := []string{
		fmt.Sprintf("project=%s env=%s site=%s machine=%s role=%s",
			d.Project, d.Environment, d.Site, d.Machine, d.Role),
		"features=[" + strings.Join(features, " ") + "]",
		"services=[" + strings.Join(d.Services, " ") + "]",
	}
	return strings.Join(lines, "\n")
}
