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
// # The platform block is the Primary Instance
//
// Every machine deploys a Primary Instance and only some deploy a Standby
// Instance, so the blocks directly under platform state the Primary Instance's
// policy and the standby block restates the same three for the Standby Instance.
// A machine that opts out of a standby states that once, with disabled, and
// authors nothing further.
//
// The two instances are independent runtimes that run at the same time on one
// host. Every port either of them binds is therefore its own: the api endpoint it
// serves and the Event Fabric ports its own NATS server binds. Nothing on a
// machine is shared between them except the ownership object, which is not a
// port.
type Platform struct {
	// API is the Primary Instance's local API endpoint policy.
	API *API `hcl:"api,block"`
	// WinService is the Primary Instance's Windows Service identity.
	WinService *WinService `hcl:"winservice,block"`
	// Nats is the Primary Instance's Event Fabric NATS port policy.
	Nats *Nats `hcl:"nats,block"`
	// Standby is the machine's local redundancy policy, and where a deployed
	// Standby Instance states its own api, winservice, and nats.
	Standby *Standby `hcl:"standby,block"`
	// Fence is the machine's optional local ownership policy. An omitted block
	// leaves the namespace at its default.
	Fence *Fence `hcl:"fence,block"`
}

// API is one instance's local API endpoint policy.
//
// Only the port is authored. The builder joins it with the machine's ip to derive
// the address the instance serves on, the same split the nats block follows: a
// blueprint states what a machine needs open, and the builder derives what
// reaches it.
//
// Each instance has its own port because both bind theirs for their whole
// lifetime, not only while Active. That is what lets an operator ask a Standby
// Instance about itself, which a single endpoint owned by whoever is Active
// cannot answer.
type API struct {
	// Port is the port this instance serves its local API on.
	Port int `hcl:"port"`
}

// maxWinServiceName bounds a Windows Service name. The Service Control Manager
// limit is 256 characters.
const maxWinServiceName = 256

// WinService is one instance's Windows Service identity.
//
// It states what the service running an instance is called, so the two fixed
// roles are recognizable in a services list and named the same way on every
// machine. This is the operator-facing half of the Fixed-Role Primary/Standby
// model: the role is fixed, and so is the service that runs it.
//
// # Declaration only
//
// The platform does not install, start, stop, or otherwise manage Windows
// Services, and it has no Service Control Manager integration. These fields are
// carried through to the deployment manifest for whoever installs the services,
// and they are a stated intention that the platform will run under the Service
// Control Manager in future. Nothing in the runtime reads them.
//
// # Why authored rather than derived
//
// The machine's ownership object is derived precisely so a blueprint cannot give
// two machines the same one, because that failure is a silent split brain. A
// service name is different: names only have to be unique on one host, two
// machines are two hosts, and a genuine collision is an installation that
// Windows refuses. The failure is loud, so readability wins and the name is
// authored.
//
// The one collision that is not loud is a machine naming its Primary and Standby
// Instances the same, since those really do share a host. The builder rejects it.
type WinService struct {
	// Name is the Windows Service name, as sc.exe and the Service Control Manager
	// use it. Required.
	Name string `hcl:"name"`
	// DisplayName is the name shown in the services list. It defaults to Name.
	DisplayName string `hcl:"display_name,optional"`
	// Description is the optional description shown in the services list.
	Description string `hcl:"description,optional"`
}

// DefaultFenceNamespace qualifies a machine's ownership object when a blueprint
// does not author one.
const DefaultFenceNamespace = "opdl"

// maxFenceNamespace bounds the authored namespace. The derived object name adds a
// namespace prefix and a fixed-width digest, and the whole name must stay inside
// the Windows kernel object name limit.
const maxFenceNamespace = 64

// Fence is a machine's local ownership policy.
//
// The machine's two processes contend for one Windows named mutex, and namespace
// is the only part of its name a blueprint authors. The object name itself is
// derived by the builder from the namespace and the machine's full identity, and
// is never authored.
//
// That split is deliberate, and it is the same rule the nats block follows. A
// blueprint that could state the object name directly could give two machines the
// same one, which would make them contend for each other's ownership, and the
// resulting descriptor would look like a working one. Authoring a namespace
// cannot cause that: the machine identity is always hashed in.
//
// The namespace exists for deployments that must not share ownership objects with
// another deployment of the same identity on the same host, such as a test rig
// running two copies side by side. Ordinary deployments omit it.
type Fence struct {
	// Namespace qualifies this machine's ownership object. It defaults to
	// DefaultFenceNamespace.
	//
	// Unlike the standby decision, a default here is safe and therefore allowed:
	// omitting it cannot make two machines share ownership, because their identities
	// still differ. Omitting a standby decision could silently deploy redundancy
	// nobody asked for, which is why that one is mandatory and this one is not.
	Namespace string `hcl:"namespace,optional"`
}

// FenceNamespace returns the machine's authored ownership namespace, or the
// default when the blueprint does not state one.
func (m Machine) FenceNamespace() string {
	if m.Platform == nil || m.Platform.Fence == nil || strings.TrimSpace(m.Platform.Fence.Namespace) == "" {
		return DefaultFenceNamespace
	}
	return strings.TrimSpace(m.Platform.Fence.Namespace)
}

// Standby is a machine's local redundancy policy, and the Standby Instance's own
// policy when one is deployed.
//
// A deployed Standby Instance states the same three blocks the Primary Instance
// states directly under platform, because it is an independent runtime and owns
// its own endpoints. All three are required when it is deployed and rejected when
// it is not: authoring an endpoint for an instance the machine does not run
// states a decision that can never take effect, and a reader could not tell it
// from one that does.
type Standby struct {
	// Disabled opts the machine out of a second local process. It is required, so
	// omitting the attribute cannot silently enable or disable redundancy.
	Disabled bool `hcl:"disabled"`
	// API is the Standby Instance's local API endpoint policy.
	API *API `hcl:"api,block"`
	// WinService is the Standby Instance's Windows Service identity.
	WinService *WinService `hcl:"winservice,block"`
	// Nats is the Standby Instance's Event Fabric NATS port policy. Its server is
	// a cluster member in its own right, so on a storage machine the two instances
	// run two servers that route to each other.
	Nats *Nats `hcl:"nats,block"`
}

// Nats is one instance's Event Fabric NATS port policy, authored so a blueprint
// reader sees which ports the machine needs open.
//
// Only ports are authored. The builder joins each port with the machine's ip to
// derive the addresses that reach this instance, and derives the site's route and
// server lists from the site topology. Authoring those lists directly could
// silently split a site or point an instance at another site's journal.
type Nats struct {
	// ClientPort is the port this instance's server serves the NATS client
	// protocol on. It is bound only on a storage machine; every other instance of
	// the site reaches the journal through the storage machines' client ports.
	ClientPort int `hcl:"client_port"`
	// ClusterPort is the port this instance's server routes to the site's other
	// storage servers on, including its own machine's other instance. It carries
	// the server-to-server route protocol only, and is bound only when the site
	// has a second storage server to route to.
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

// validatePlatform checks a machine states every platform policy. None has a
// default: an omitted api or nats block would leave an instance without an
// endpoint, and an omitted standby block would make local redundancy depend on
// what a reader assumed rather than on what the blueprint says.
func validatePlatform(machine Machine) error {
	if machine.Platform == nil {
		return fmt.Errorf("machine %q: platform block is required", machine.Name)
	}
	if machine.Platform.Standby == nil {
		return fmt.Errorf("machine %q: platform.standby block is required", machine.Name)
	}
	if err := validateInstanceEndpoints(machine, "platform", machine.Platform.API, machine.Platform.Nats); err != nil {
		return err
	}
	if err := validateStandbyEndpoints(machine); err != nil {
		return err
	}
	if err := validateMachinePorts(machine); err != nil {
		return err
	}
	if err := validateWinServices(machine); err != nil {
		return err
	}
	return validateFence(machine)
}

// validateInstanceEndpoints checks one instance states both of its endpoint
// policies with usable ports.
func validateInstanceEndpoints(machine Machine, block string, api *API, nats *Nats) error {
	if api == nil {
		return fmt.Errorf("machine %q: %s.api block is required", machine.Name, block)
	}
	if err := validatePort(machine.Name, block+".api.port", api.Port); err != nil {
		return err
	}
	if nats == nil {
		return fmt.Errorf("machine %q: %s.nats block is required", machine.Name, block)
	}
	if err := validatePort(machine.Name, block+".nats.client_port", nats.ClientPort); err != nil {
		return err
	}
	return validatePort(machine.Name, block+".nats.cluster_port", nats.ClusterPort)
}

// validateStandbyEndpoints checks a deployed Standby Instance states its own
// endpoints, and that a machine which opts out of a standby states nothing for
// one.
func validateStandbyEndpoints(machine Machine) error {
	standby := machine.Platform.Standby
	if !standby.Disabled {
		return validateInstanceEndpoints(machine, "platform.standby", standby.API, standby.Nats)
	}
	if standby.API != nil {
		return fmt.Errorf("machine %q: platform.standby.api is set but the standby is disabled; remove it or deploy the standby", machine.Name)
	}
	if standby.Nats != nil {
		return fmt.Errorf("machine %q: platform.standby.nats is set but the standby is disabled; remove it or deploy the standby", machine.Name)
	}
	return nil
}

// validateMachinePorts checks no two listeners on the machine are given the same
// port.
//
// Every port a machine authors is bound on one host at one time: the two
// instances are independent runtimes that run together, and each binds its own
// api endpoint and, on a storage machine, its own NATS client and cluster
// listeners. There is no ownership rule that makes any pair of them mutually
// exclusive, so a repeated port is a listener that will fail to bind.
//
// This is the authoring mistake the six-port shape invites, and copying the
// primary's block into standby is how it happens, so the message names both
// listeners rather than only reporting a duplicate.
func validateMachinePorts(machine Machine) error {
	type listener struct {
		where string
		port  int
	}
	platform := machine.Platform
	listeners := []listener{
		{"platform.api.port", platform.API.Port},
		{"platform.nats.client_port", platform.Nats.ClientPort},
		{"platform.nats.cluster_port", platform.Nats.ClusterPort},
	}
	if !platform.Standby.Disabled {
		listeners = append(listeners,
			listener{"platform.standby.api.port", platform.Standby.API.Port},
			listener{"platform.standby.nats.client_port", platform.Standby.Nats.ClientPort},
			listener{"platform.standby.nats.cluster_port", platform.Standby.Nats.ClusterPort},
		)
	}
	taken := make(map[int]string, len(listeners))
	for _, l := range listeners {
		if owner, used := taken[l.port]; used {
			return fmt.Errorf("machine %q: %s and %s are both %d; every listener on a machine needs its own port", machine.Name, owner, l.where, l.port)
		}
		taken[l.port] = l.where
	}
	return nil
}

// validateWinServices checks each deployed instance states a usable Windows
// Service identity, and only a deployed instance states one.
func validateWinServices(machine Machine) error {
	primary := machine.Platform.WinService
	if primary == nil {
		return fmt.Errorf("machine %q: platform.winservice block is required; every machine deploys a Primary Instance", machine.Name)
	}
	if err := validateWinService(machine.Name, "platform.winservice", primary); err != nil {
		return err
	}

	standby := machine.Platform.Standby.WinService
	if machine.Platform.Standby.Disabled {
		if standby != nil {
			return fmt.Errorf("machine %q: platform.standby.winservice is set but the standby is disabled; remove it or deploy the standby", machine.Name)
		}
		return nil
	}
	if standby == nil {
		return fmt.Errorf("machine %q: platform.standby.winservice block is required when the standby is deployed", machine.Name)
	}
	if err := validateWinService(machine.Name, "platform.standby.winservice", standby); err != nil {
		return err
	}
	// The two instances share one host, so this is the one service-name collision
	// that cannot be caught at install time by Windows refusing a duplicate.
	if primary.Name == standby.Name {
		return fmt.Errorf("machine %q: the Primary and Standby Instances both name their service %q; they run on one host and must differ", machine.Name, primary.Name)
	}
	return nil
}

// validateWinService checks one authored service identity. The rules are the
// Service Control Manager's, checked at build time so a package cannot ship a
// name that installation would reject.
func validateWinService(machineName, block string, service *WinService) error {
	name := service.Name
	switch {
	case strings.TrimSpace(name) == "":
		return fmt.Errorf("machine %q: %s.name is required", machineName, block)
	case name != strings.TrimSpace(name):
		return fmt.Errorf("machine %q: %s.name %q must not have leading or trailing whitespace", machineName, block, name)
	case len(name) > maxWinServiceName:
		return fmt.Errorf("machine %q: %s.name %q is longer than %d characters", machineName, block, name, maxWinServiceName)
	// The Service Control Manager rejects both slashes in a service name.
	case strings.ContainsAny(name, `/\`):
		return fmt.Errorf("machine %q: %s.name %q must not contain a slash or backslash", machineName, block, name)
	}
	if len(service.DisplayName) > maxWinServiceName {
		return fmt.Errorf("machine %q: %s.display_name is longer than %d characters", machineName, block, maxWinServiceName)
	}
	return nil
}

// WinServiceIdentity resolves one instance's service identity, filling the
// display name default. It returns nil when the instance is not deployed.
func (m Machine) WinServiceIdentity(standby bool) *WinService {
	if m.Platform == nil {
		return nil
	}
	authored := m.Platform.WinService
	if standby {
		if m.Platform.Standby == nil || m.Platform.Standby.Disabled {
			return nil
		}
		authored = m.Platform.Standby.WinService
	}
	if authored == nil {
		return nil
	}
	resolved := *authored
	if strings.TrimSpace(resolved.DisplayName) == "" {
		resolved.DisplayName = resolved.Name
	}
	return &resolved
}

// validateFence checks an authored ownership namespace is usable in a Windows
// kernel object name. A blank block is rejected rather than silently defaulted:
// authoring fence {} with an empty namespace states a decision that cannot be
// honored, and treating it as "use the default" would hide the mistake.
func validateFence(machine Machine) error {
	if machine.Platform.Fence == nil {
		return nil
	}
	namespace := machine.Platform.Fence.Namespace
	if strings.TrimSpace(namespace) == "" {
		return fmt.Errorf("machine %q: platform.fence.namespace must not be blank; omit the fence block to use %q", machine.Name, DefaultFenceNamespace)
	}
	if namespace != strings.TrimSpace(namespace) {
		return fmt.Errorf("machine %q: platform.fence.namespace %q must not have leading or trailing whitespace", machine.Name, namespace)
	}
	if len(namespace) > maxFenceNamespace {
		return fmt.Errorf("machine %q: platform.fence.namespace %q is longer than %d characters", machine.Name, namespace, maxFenceNamespace)
	}
	// A backslash would escape the kernel namespace the platform places the object
	// in, which is how a machine could reach outside Global\ or collide by design.
	if strings.ContainsAny(namespace, `\/`) {
		return fmt.Errorf("machine %q: platform.fence.namespace %q must not contain a slash or backslash", machine.Name, namespace)
	}
	return nil
}

func validatePort(machineName, where string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("machine %q: %s must be in range 1-65535, got %d", machineName, where, port)
	}
	return nil
}

// Endpoints resolves one instance's authored endpoint ports, or nil when that
// instance is not deployed. It is the one place resolution asks a machine which
// ports an instance owns, so the primary-under-platform and standby-under-standby
// asymmetry is read in a single place rather than repeated per endpoint.
func (m Machine) Endpoints(standby bool) *Endpoints {
	if m.Platform == nil {
		return nil
	}
	api, nats := m.Platform.API, m.Platform.Nats
	if standby {
		if m.Platform.Standby == nil || m.Platform.Standby.Disabled {
			return nil
		}
		api, nats = m.Platform.Standby.API, m.Platform.Standby.Nats
	}
	if api == nil || nats == nil {
		return nil
	}
	return &Endpoints{APIPort: api.Port, ClientPort: nats.ClientPort, ClusterPort: nats.ClusterPort}
}

// Endpoints are one instance's authored listener ports.
type Endpoints struct {
	APIPort     int
	ClientPort  int
	ClusterPort int
}
