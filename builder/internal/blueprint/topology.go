package blueprint

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"
)

// The hcl struct tags map HCL attributes and blocks onto fields. The "label"
// tag captures a block's name label (the "customer-a" in project "customer-a").

// Project is the top of a topology: one project (customer), the environment
// common to it, and the sites and machines nested inside.
type Project struct {
	// Name is the project (customer) identifier.
	Name string `hcl:"name,label"`
	// Environment is the target environment, e.g. "production".
	Environment string `hcl:"environment"`
	// Sites are the locations the project is deployed to.
	Sites []Site `hcl:"site,block"`
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
	// MachineProfile is the machine's purpose, e.g. "sensor-node".
	MachineProfile string `hcl:"profile"`
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
	// DataDir is the Primary Instance's general platform data root. Required.
	DataDir string `hcl:"data_dir,optional"`
	// API is the Primary Instance's local API endpoint policy.
	API *API `hcl:"api,block"`
	// WinService is the Primary Instance's Windows Service identity.
	WinService *WinService `hcl:"winservice,block"`
	// Standby is the machine's local redundancy policy, and where a deployed
	// Standby Instance states its own lock, data_dir, api, and winservice.
	Standby *Standby `hcl:"standby,block"`
}

// API is one instance's local API endpoint policy.
//
// # The API is machine-local
//
// Only the port is authored, and it is named local_port because the builder joins
// it with 127.0.0.1 and never with the machine's ip. The platform API is how an
// operator, or a service co-located on that host, asks this instance about
// itself; it is not how machines reach each other. Cross-machine traffic is the
// Event Fabric's, and the nats block is where the ports derived from the machine
// ip are authored.
//
// The name carries the constraint so a blueprint cannot be authored in the belief
// that this port will be reachable from the network. Nothing binds it off
// loopback, and the resolved descriptor is validated to say so.
//
// Each instance has its own port because both bind theirs for their whole
// lifetime, not only while Active. That is what lets an operator ask a Standby
// Instance about itself, which a single endpoint owned by whoever is Active
// cannot answer.
type API struct {
	// LocalPort is the loopback port this instance serves its local API on.
	LocalPort int `hcl:"local_port"`
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

// Lease is a machine's local Primary Ownership lease policy.
//
// A machine's primary and standby processes coordinate Primary Ownership through
// a lease record in a shared machine-wide file rather than a kernel object. The
// file path and the lease timings are authored here so an operator reading the
// blueprint sees exactly where ownership is recorded and how quickly it turns
// over.
//
// The durations are HCL strings in Go's duration syntax ("15s", "5s"). They are
// carried through the descriptor unparsed and validated by the builder; the
// platform parses them at startup.
type Lease struct {
	// File is the machine-wide lease file both instances read and write. It is an
	// absolute path on a local filesystem, shared by the machine's two instances
	// and by nothing else.
	File string `hcl:"file"`
	// Duration is how long a granted lease is valid without renewal.
	Duration string `hcl:"duration"`
	// RenewalInterval is how often the owner extends the lease. It must be well
	// below Duration.
	RenewalInterval string `hcl:"renewal_interval"`
	// HealthCheckInterval is how often a Passive instance evaluates promotion and
	// polls its peer's health.
	HealthCheckInterval string `hcl:"health_check_interval"`
	// FailbackStabilization is how long a returning Primary must be continuously
	// healthy before an Active Standby hands ownership back to it.
	FailbackStabilization string `hcl:"failback_stabilization"`
}

// Lease returns the machine's authored ownership lease policy, or nil when the
// blueprint does not deploy a standby or states no lease.
func (m Machine) Lease() *Lease {
	if m.Platform == nil || m.Platform.Standby == nil || m.Platform.Standby.Disabled {
		return nil
	}
	return m.Platform.Standby.Lease
}

// Standby is a machine's local redundancy policy, and the Standby Instance's own
// policy when one is deployed.
//
// A deployed Standby Instance states the same blocks the Primary Instance
// states directly under platform, because it is an independent runtime and owns
// its own endpoints. All are required when it is deployed and rejected when
// it is not: authoring an endpoint for an instance the machine does not run
// states a decision that can never take effect, and a reader could not tell it
// from one that does.
type Standby struct {
	// Disabled opts the machine out of a second local process. It is required, so
	// omitting the attribute cannot silently enable or disable redundancy.
	Disabled bool `hcl:"disabled"`
	// DataDir is the Standby Instance's general platform data root. It is
	// required when the Standby Instance is deployed and rejected when it is not.
	DataDir string `hcl:"data_dir,optional"`
	// Lease is the machine's local Primary Ownership lease policy. It is required
	// when the Standby Instance is deployed and rejected when it is not.
	Lease *Lease `hcl:"lease,block"`
	// API is the Standby Instance's local API endpoint policy.
	API *API `hcl:"api,block"`
	// WinService is the Standby Instance's Windows Service identity.
	WinService *WinService `hcl:"winservice,block"`
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

	if strings.TrimSpace(machine.MachineProfile) == "" {
		return fmt.Errorf("machine %q: profile is required", machine.Name)
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
// default: an omitted api block would leave an instance without an endpoint,
// and an omitted standby block would make local redundancy depend on what a
// reader assumed rather than on what the blueprint says.
func validatePlatform(machine Machine) error {
	if machine.Platform == nil {
		return fmt.Errorf("machine %q: platform block is required", machine.Name)
	}
	if machine.Platform.Standby == nil {
		return fmt.Errorf("machine %q: platform.standby block is required", machine.Name)
	}
	if err := validateInstanceEndpoints(machine, "platform", machine.Platform.API); err != nil {
		return err
	}
	if err := validateStandbyEndpoints(machine); err != nil {
		return err
	}
	if err := validateDataDirs(machine); err != nil {
		return err
	}
	if err := validateMachinePorts(machine); err != nil {
		return err
	}
	if err := validateWinServices(machine); err != nil {
		return err
	}
	return validateLease(machine)
}

// validateInstanceEndpoints checks one instance states its endpoint
// policies with usable ports.
func validateInstanceEndpoints(machine Machine, block string, api *API) error {
	if api == nil {
		return fmt.Errorf("machine %q: %s.api block is required", machine.Name, block)
	}
	return validatePort(machine.Name, block+".api.local_port", api.LocalPort)
}

// validateStandbyEndpoints checks a deployed Standby Instance states its own
// endpoints, and that a machine which opts out of a standby states nothing for
// one.
func validateStandbyEndpoints(machine Machine) error {
	standby := machine.Platform.Standby
	if !standby.Disabled {
		return validateInstanceEndpoints(machine, "platform.standby", standby.API)
	}
	if strings.TrimSpace(standby.DataDir) != "" {
		return fmt.Errorf("machine %q: platform.standby.data_dir is set but the standby is disabled; remove it or deploy the standby", machine.Name)
	}
	if standby.Lease != nil {
		return fmt.Errorf("machine %q: platform.standby.lease is set but the standby is disabled; remove it or deploy the standby", machine.Name)
	}
	if standby.API != nil {
		return fmt.Errorf("machine %q: platform.standby.api is set but the standby is disabled; remove it or deploy the standby", machine.Name)
	}
	return nil
}

// validateDataDirs checks each deployed instance states its own platform data
// root, and that a machine's two instances do not state the same one.
func validateDataDirs(machine Machine) error {
	if strings.TrimSpace(machine.Platform.DataDir) == "" {
		return fmt.Errorf("machine %q: platform.data_dir is required", machine.Name)
	}
	standby := machine.Platform.Standby
	if standby == nil || standby.Disabled {
		return nil
	}
	if strings.TrimSpace(standby.DataDir) == "" {
		return fmt.Errorf("machine %q: platform.standby.data_dir is required", machine.Name)
	}
	if filepath.Clean(machine.Platform.DataDir) == filepath.Clean(standby.DataDir) {
		return fmt.Errorf("machine %q: platform.data_dir and platform.standby.data_dir: the two instances run together and cannot share a platform data directory", machine.Name)
	}
	return nil
}

func validateMachinePorts(machine Machine) error {
	type listener struct {
		where string
		port  int
	}
	platform := machine.Platform
	listeners := []listener{
		{"platform.api.local_port", platform.API.LocalPort},
	}
	if !platform.Standby.Disabled {
		listeners = append(listeners,
			listener{"platform.standby.api.local_port", platform.Standby.API.LocalPort},
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

// validateLease checks the Primary Ownership lease is authored on a standby
// machine and omitted on a standby-disabled machine, and that its file and
// timings are usable.
func validateLease(machine Machine) error {
	standby := machine.Platform.Standby
	if standby.Disabled {
		return nil
	}
	if standby.Lease == nil {
		return fmt.Errorf("machine %q: platform.standby.lease block is required when the standby is deployed", machine.Name)
	}
	lease := standby.Lease
	file := strings.TrimSpace(lease.File)
	if file == "" {
		return fmt.Errorf("machine %q: platform.standby.lease.file is required", machine.Name)
	}
	if lease.File != file {
		return fmt.Errorf("machine %q: platform.standby.lease.file %q must not have leading or trailing whitespace", machine.Name, lease.File)
	}
	duration, err := validateLeaseDuration(machine.Name, "duration", lease.Duration)
	if err != nil {
		return err
	}
	renewal, err := validateLeaseDuration(machine.Name, "renewal_interval", lease.RenewalInterval)
	if err != nil {
		return err
	}
	if _, err := validateLeaseDuration(machine.Name, "health_check_interval", lease.HealthCheckInterval); err != nil {
		return err
	}
	if _, err := validateLeaseDuration(machine.Name, "failback_stabilization", lease.FailbackStabilization); err != nil {
		return err
	}
	// Renewal must fit comfortably inside the lease so at least two attempts land
	// before expiry; the platform derives its step-down grace from the difference.
	if renewal >= duration {
		return fmt.Errorf("machine %q: platform.standby.lease.renewal_interval %s must be shorter than duration %s", machine.Name, lease.RenewalInterval, lease.Duration)
	}
	return nil
}

// validateLeaseDuration parses one authored lease duration and requires it to be
// a positive Go duration.
func validateLeaseDuration(machineName, attribute, value string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return 0, fmt.Errorf("machine %q: platform.standby.lease.%s is required", machineName, attribute)
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("machine %q: platform.standby.lease.%s %q is not a valid duration: %w", machineName, attribute, value, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("machine %q: platform.standby.lease.%s %s must be positive", machineName, attribute, d)
	}
	return d, nil
}

func validatePort(machineName, where string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("machine %q: %s must be in range 1-65535, got %d", machineName, where, port)
	}
	return nil
}

// Endpoints resolves one instance's authored endpoint ports, or nil when that
// instance is not deployed.
func (m Machine) Endpoints(standby bool) *Endpoints {
	if m.Platform == nil {
		return nil
	}
	api := m.Platform.API
	if standby {
		if m.Platform.Standby == nil || m.Platform.Standby.Disabled {
			return nil
		}
		api = m.Platform.Standby.API
	}
	if api == nil {
		return nil
	}
	return &Endpoints{APILocalPort: api.LocalPort}
}

// Endpoints are one instance's authored listener ports.
type Endpoints struct {
	APILocalPort int
}

// DataDir returns one instance's authored general platform data directory, or
// an empty string when that instance is not deployed.
func (m Machine) DataDir(standby bool) string {
	if m.Platform == nil {
		return ""
	}
	if !standby {
		return strings.TrimSpace(m.Platform.DataDir)
	}
	if m.Platform.Standby == nil || m.Platform.Standby.Disabled {
		return ""
	}
	return strings.TrimSpace(m.Platform.Standby.DataDir)
}
