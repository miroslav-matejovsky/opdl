package blueprint

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// The hcl struct tags map HCL attributes and blocks onto fields. The "label"
// tag captures a block's name label (the "customer-a" in project "customer-a").

// Project is the top of a topology: one project (customer), the environment
// and feature switches common to it, and the sites and machines nested
// inside.
type Project struct {
	// Name is the project (customer) identifier.
	Name string `hcl:"name,label"`
	// Environment is the target environment, e.g. "production".
	Environment string `hcl:"environment"`
	// Features are the capability switches available to the project.
	Features Features `hcl:"features,block"`
	// Sites are the locations the project is deployed to.
	Sites []Site `hcl:"site,block"`
}

// Features are the project-level capability switches.
type Features struct {
	// Chaos enables deliberately injecting failures to test the system's
	// resilience: in test environments freely, or in production in a
	// controlled way with the customer aware of the test and its potential
	// impact on their operations.
	Chaos bool `hcl:"chaos,optional"`
}

// Site is one location within a project holding a set of machines.
type Site struct {
	// Name is the site identifier, unique within its project.
	Name string `hcl:"name,label"`
	// Machines are the deployable units placed at this site.
	Machines []Machine `hcl:"machine,block"`
}

// Machine is one deployable unit within a site.
type Machine struct {
	// Name is the machine identifier, unique within its project.
	Name string `hcl:"name,label"`
	// Role is the machine's role, e.g. "sensor-node".
	Role string `hcl:"role"`
	// IP is the machine's network address, e.g. "10.0.1.10".
	IP string `hcl:"ip"`
	// Services lists the service groups assigned to this machine.
	Services []string `hcl:"services"`
	// Platform contains the explicitly authored platform process topology. It
	// is represented as a slice so validation can report a missing or duplicate
	// block clearly.
	Platform []Platform `hcl:"platform,block"`
}

// Platform is one machine's authored platform process topology. Production
// endpoints have no defaults: every enabled instance must be described here.
type Platform struct {
	// SecondaryEnabled controls whether the secondary instance is present. Nil
	// means enabled, which makes redundancy the default while retaining an
	// explicit false value.
	SecondaryEnabled *bool `hcl:"secondary_enabled,optional"`
	// Instances contains the explicitly addressed primary and optional
	// secondary platform processes.
	Instances []PlatformInstance `hcl:"instance,block"`
}

// PlatformInstance is one authored active platform process and its endpoints.
// Name is restricted by validation to primary or secondary.
type PlatformInstance struct {
	Name                    string `hcl:"name,label"`
	APIAddress              string `hcl:"api_address"`
	FabricClientAddress     string `hcl:"fabric_client_address"`
	FabricMemberlistAddress string `hcl:"fabric_memberlist_address"`
}

const (
	// PlatformInstancePrimary is the required platform process identity.
	PlatformInstancePrimary = "primary"
	// PlatformInstanceSecondary is the optional redundant process identity.
	PlatformInstanceSecondary = "secondary"
)

// SecondaryIsEnabled reports the authored secondary setting after applying its
// default. Secondary is enabled unless explicitly disabled.
func (p Platform) SecondaryIsEnabled() bool {
	return p.SecondaryEnabled == nil || *p.SecondaryEnabled
}

// PlatformInstances returns the validated machine instances in canonical
// primary, then secondary order. Call it only after Project.Validate succeeds.
func (m Machine) PlatformInstances() []PlatformInstance {
	if len(m.Platform) != 1 {
		return nil
	}
	byName := make(map[string]PlatformInstance, len(m.Platform[0].Instances))
	for _, instance := range m.Platform[0].Instances {
		byName[instance.Name] = instance
	}
	instances := []PlatformInstance{byName[PlatformInstancePrimary]}
	if m.Platform[0].SecondaryIsEnabled() {
		instances = append(instances, byName[PlatformInstanceSecondary])
	}
	return instances
}

// Validate checks a project against the model's structural rules. It fails
// fast on the first violation. Identity must be present, and site, machine,
// and service names must be unique within their scope.
func (p *Project) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("project: name is required")
	}
	if strings.TrimSpace(p.Environment) == "" {
		return fmt.Errorf("project %q: environment is required", p.Name)
	}
	if len(p.Sites) == 0 {
		return fmt.Errorf("project %q: at least one site is required", p.Name)
	}

	siteNames := make(map[string]bool, len(p.Sites))
	machineNames := make(map[string]bool)
	machineIPs := make(map[string]string)
	endpointOwners := make(map[string]string)
	for _, site := range p.Sites {
		if strings.TrimSpace(site.Name) == "" {
			return fmt.Errorf("project %q: site with empty name", p.Name)
		}
		if siteNames[site.Name] {
			return fmt.Errorf("project %q: duplicate site %q", p.Name, site.Name)
		}
		siteNames[site.Name] = true

		if len(site.Machines) == 0 {
			return fmt.Errorf("site %q: at least one machine is required", site.Name)
		}
		for _, machine := range site.Machines {
			if err := p.validateMachine(site, machine, machineNames); err != nil {
				return err
			}
			// An IP must identify exactly one machine in the project. The
			// platform derives its fabric addresses from a machine's IP on fixed
			// ports, so two machines sharing an IP would derive the same
			// addresses and could not both bind them.
			if owner, taken := machineIPs[machine.IP]; taken {
				return fmt.Errorf("project %q: machines %q and %q share ip %q", p.Name, owner, machine.Name, machine.IP)
			}
			machineIPs[machine.IP] = machine.Name
			for _, instance := range machine.PlatformInstances() {
				owner := machine.Name + "/" + instance.Name
				for _, endpoint := range instanceEndpoints(instance) {
					field, address := endpoint.field, endpoint.address
					if existing, taken := endpointOwners[address]; taken {
						return fmt.Errorf("project %q: platform endpoint %q for %s %s is already used by %s", p.Name, address, owner, field, existing)
					}
					endpointOwners[address] = owner + " " + field
				}
			}
		}
	}
	return nil
}

// validateMachine checks one machine's identity and services. Machine names
// must be unique across the whole project because each machine is a distinct
// deployment identity.
func (p *Project) validateMachine(site Site, machine Machine, machineNames map[string]bool) error {
	if strings.TrimSpace(machine.Name) == "" {
		return fmt.Errorf("site %q: machine with empty name", site.Name)
	}
	if machineNames[machine.Name] {
		return fmt.Errorf("project %q: duplicate machine %q", p.Name, machine.Name)
	}
	machineNames[machine.Name] = true

	if strings.TrimSpace(machine.Role) == "" {
		return fmt.Errorf("machine %q: role is required", machine.Name)
	}
	if strings.TrimSpace(machine.IP) == "" {
		return fmt.Errorf("machine %q: ip is required", machine.Name)
	}
	if net.ParseIP(machine.IP) == nil {
		return fmt.Errorf("machine %q: ip %q is not a valid IP address", machine.Name, machine.IP)
	}
	if len(machine.Services) == 0 {
		return fmt.Errorf("machine %q: at least one service is required", machine.Name)
	}

	assigned := make(map[string]bool, len(machine.Services))
	for _, name := range machine.Services {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("machine %q: service with empty name", machine.Name)
		}
		if assigned[name] {
			return fmt.Errorf("machine %q: service %q assigned more than once", machine.Name, name)
		}
		assigned[name] = true
	}
	return validatePlatform(machine)
}

// validatePlatform checks that a machine explicitly defines every enabled
// process and endpoint. No address or port is invented by production code.
func validatePlatform(machine Machine) error {
	if len(machine.Platform) != 1 {
		return fmt.Errorf("machine %q: exactly one platform block is required, found %d", machine.Name, len(machine.Platform))
	}
	platform := machine.Platform[0]
	instances := make(map[string]bool, len(platform.Instances))
	for _, instance := range platform.Instances {
		if instance.Name != PlatformInstancePrimary && instance.Name != PlatformInstanceSecondary {
			return fmt.Errorf("machine %q: platform instance %q is invalid; expected primary or secondary", machine.Name, instance.Name)
		}
		if instances[instance.Name] {
			return fmt.Errorf("machine %q: duplicate platform instance %q", machine.Name, instance.Name)
		}
		instances[instance.Name] = true
		for _, endpoint := range instanceEndpoints(instance) {
			field, address := endpoint.field, endpoint.address
			if err := validateEndpoint(address); err != nil {
				return fmt.Errorf("machine %q: platform instance %q %s: %w", machine.Name, instance.Name, field, err)
			}
		}
	}
	if !instances[PlatformInstancePrimary] {
		return fmt.Errorf("machine %q: primary platform instance is required", machine.Name)
	}
	if platform.SecondaryIsEnabled() && !instances[PlatformInstanceSecondary] {
		return fmt.Errorf("machine %q: secondary platform instance is enabled but not defined", machine.Name)
	}
	if !platform.SecondaryIsEnabled() && instances[PlatformInstanceSecondary] {
		return fmt.Errorf("machine %q: secondary platform instance is defined but disabled", machine.Name)
	}
	want := 1
	if platform.SecondaryIsEnabled() {
		want = 2
	}
	if len(platform.Instances) != want {
		return fmt.Errorf("machine %q: expected %d platform instances, found %d", machine.Name, want, len(platform.Instances))
	}
	return nil
}

type namedEndpoint struct {
	field   string
	address string
}

func instanceEndpoints(instance PlatformInstance) []namedEndpoint {
	return []namedEndpoint{
		{field: "api_address", address: instance.APIAddress},
		{field: "fabric_client_address", address: instance.FabricClientAddress},
		{field: "fabric_memberlist_address", address: instance.FabricMemberlistAddress},
	}
}

// validateEndpoint checks an explicitly authored endpoint is a usable
// host:port. Hostnames and IP literals are both allowed; empty hosts are not.
func validateEndpoint(address string) error {
	if strings.TrimSpace(address) != address || address == "" {
		return fmt.Errorf("address %q must be a non-blank host:port without surrounding whitespace", address)
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("address %q must be host:port: %w", address, err)
	}
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("address %q has no host", address)
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("address %q port is not a number", address)
	}
	if number < 1 || number > 65535 {
		return fmt.Errorf("address %q port %d is out of range 1-65535", address, number)
	}
	return nil
}
