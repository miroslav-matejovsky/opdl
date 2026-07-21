package blueprint

import (
	"fmt"
	"net"
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
//
// Both subsections are mandatory. A machine that omits either one is rejected,
// so redundancy and the Event Fabric's ports are always a stated decision rather
// than an inherited default.
type Platform struct {
	// Nats is the machine's Event Fabric NATS port policy. It is machine-level:
	// whichever process holds the machine fence binds these ports, so the primary
	// and its standby never own separate endpoints.
	Nats *Nats `hcl:"nats,block"`
	// Standby is the machine's local redundancy policy.
	Standby *Standby `hcl:"standby,block"`
}

// Standby is a machine's local redundancy policy.
type Standby struct {
	// Disabled opts the machine out of a second local process. It is required, so
	// omitting the attribute cannot silently enable or disable redundancy.
	//
	// A false value deploys a second local process that waits on the machine
	// fence. It does not add a second NATS endpoint: the two processes are
	// mutually exclusive owners of the same machine-level ports.
	Disabled bool `hcl:"disabled"`
}

// Nats is a machine's Event Fabric NATS port policy. It is authored inside
// platform {} so a blueprint reader sees which ports the machine needs open.
//
// Only ports are authored. The builder joins each port with the machine's ip to
// derive the addresses that reach it, and derives the site's route and server
// lists from the site topology. Authoring those lists directly could silently
// split a site or point a machine at another site's journal.
type Nats struct {
	// ClientPort is the port the machine's server serves the NATS client protocol
	// on. The platform's active process, a local standby following the journal,
	// and every machine of the site that does not store the journal all reach the
	// Event Fabric through it.
	ClientPort int `hcl:"client_port"`
	// ClusterPort is the port the machine's server routes to the site's other
	// storage nodes on. It carries the server-to-server route protocol only and
	// is bound only when the site topology selects three storage nodes.
	ClusterPort int `hcl:"cluster_port"`
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

	return validatePlatform(machine)
}

// validatePlatform checks a machine states both platform policies. Neither has a
// default: an omitted nats block would leave the Event Fabric without ports, and
// an omitted standby block would make local redundancy depend on what a reader
// assumed rather than on what the blueprint says.
func validatePlatform(machine Machine) error {
	if machine.Platform == nil {
		return fmt.Errorf("machine %q: platform block is required", machine.Name)
	}
	if machine.Platform.Standby == nil {
		return fmt.Errorf("machine %q: platform.standby block is required", machine.Name)
	}
	if machine.Platform.Nats == nil {
		return fmt.Errorf("machine %q: platform.nats block is required", machine.Name)
	}
	nats := machine.Platform.Nats
	if err := validatePort(machine.Name, "client_port", nats.ClientPort); err != nil {
		return err
	}
	if err := validatePort(machine.Name, "cluster_port", nats.ClusterPort); err != nil {
		return err
	}
	// One listener per port. The client and route protocols are different
	// protocols on the same server, so a shared port would leave the server
	// unable to bind the second of them.
	if nats.ClientPort == nats.ClusterPort {
		return fmt.Errorf("machine %q: platform.nats.client_port and cluster_port must differ, both are %d", machine.Name, nats.ClientPort)
	}
	return nil
}

func validatePort(machineName, what string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("machine %q: platform.nats.%s must be in range 1-65535, got %d", machineName, what, port)
	}
	return nil
}
