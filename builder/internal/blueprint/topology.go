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
	// Platform is the optional platform-runtime policy subsection. An omitted
	// block leaves every platform policy at its default.
	Platform *Platform `hcl:"platform,block"`
}

// Platform is a machine's platform-runtime policy: how the platform runs on the
// machine, as opposed to what it deploys. It is authored as a platform {}
// subsection of a machine so a blueprint reader sees these policies grouped and
// explicit rather than mixed in with the machine's identity and services.
type Platform struct {
	// Nats is the primary slot's explicit Event Fabric NATS configuration.
	Nats *Nats `hcl:"nats,block"`
	// Standby is the optional standby slot policy. When provided, its Nats block must be filled.
	Standby *Standby `hcl:"standby,block"`
}

// Standby holds the optional standby slot configuration subsection.
type Standby struct {
	Nats *Nats `hcl:"nats,block"`
}

// Nats is a machine's Event Fabric NATS configuration. It is authored inside
// platform {} so the runtime addresses are explicit right in the blueprint.
type Nats struct {
	ClientAddress  string   `hcl:"client_address"`
	ClusterAddress string   `hcl:"cluster_address"`
	MonitorAddress string   `hcl:"monitor_address"`
	Routes         []string `hcl:"routes,optional"`
	Servers        []string `hcl:"servers,optional"`
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

	if machine.Platform == nil || machine.Platform.Nats == nil {
		return fmt.Errorf("machine %q: platform.nats configuration is required", machine.Name)
	}
	if err := validateNatsBlock(machine.Name, "platform.nats", machine.Platform.Nats); err != nil {
		return err
	}
	var allAddrs []string
	allAddrs = append(allAddrs, machine.Platform.Nats.ClientAddress, machine.Platform.Nats.ClusterAddress, machine.Platform.Nats.MonitorAddress)
	if machine.Platform.Standby != nil {
		if machine.Platform.Standby.Nats == nil {
			return fmt.Errorf("machine %q: platform.standby requires nats block", machine.Name)
		}
		if err := validateNatsBlock(machine.Name, "platform.standby.nats", machine.Platform.Standby.Nats); err != nil {
			return err
		}
		allAddrs = append(allAddrs, machine.Platform.Standby.Nats.ClientAddress, machine.Platform.Standby.Nats.ClusterAddress, machine.Platform.Standby.Nats.MonitorAddress)
	}
	if err := uniqueAddresses(allAddrs...); err != nil {
		return fmt.Errorf("machine %q: platform.%w", machine.Name, err)
	}
	return nil
}

func validateNatsBlock(machineName, prefix string, nats *Nats) error {
	if strings.TrimSpace(nats.ClientAddress) == "" {
		return fmt.Errorf("machine %q: %s.client_address is required", machineName, prefix)
	}
	if err := validateAddress("client_address", nats.ClientAddress); err != nil {
		return fmt.Errorf("machine %q: %s.%w", machineName, prefix, err)
	}
	if strings.TrimSpace(nats.ClusterAddress) == "" {
		return fmt.Errorf("machine %q: %s.cluster_address is required", machineName, prefix)
	}
	if err := validateAddress("cluster_address", nats.ClusterAddress); err != nil {
		return fmt.Errorf("machine %q: %s.%w", machineName, prefix, err)
	}
	if strings.TrimSpace(nats.MonitorAddress) == "" {
		return fmt.Errorf("machine %q: %s.monitor_address is required", machineName, prefix)
	}
	if err := validateAddress("monitor_address", nats.MonitorAddress); err != nil {
		return fmt.Errorf("machine %q: %s.%w", machineName, prefix, err)
	}
	if err := uniqueAddresses(nats.ClientAddress, nats.ClusterAddress, nats.MonitorAddress); err != nil {
		return fmt.Errorf("machine %q: %s.%w", machineName, prefix, err)
	}
	for _, route := range nats.Routes {
		if err := validateAddress("routes", route); err != nil {
			return fmt.Errorf("machine %q: %s.%w", machineName, prefix, err)
		}
	}
	for _, server := range nats.Servers {
		if err := validateAddress("servers", server); err != nil {
			return fmt.Errorf("machine %q: %s.%w", machineName, prefix, err)
		}
	}
	return nil
}

func validateAddress(what, addr string) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%s %q must be host:port: %w", what, addr, err)
	}
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("%s %q has no host", what, addr)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("%s %q: port is not a number", what, addr)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("%s %q: port %d out of range 1-65535", what, addr, port)
	}
	return nil
}

func uniqueAddresses(addrs ...string) error {
	seen := make(map[string]bool, len(addrs))
	for _, addr := range addrs {
		if seen[addr] {
			return fmt.Errorf("address %q is used more than once", addr)
		}
		seen[addr] = true
	}
	return nil
}
