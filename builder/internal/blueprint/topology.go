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
	// RuntimeDir is the Primary Instance's local runtime directory. Required.
	RuntimeDir string `hcl:"runtime_dir,optional"`
	// DataDir is the Primary Instance's general platform data root. Required.
	DataDir string `hcl:"data_dir,optional"`
	// API is the Primary Instance's local API endpoint policy.
	API *API `hcl:"api,block"`
	// WinService is the Primary Instance's Windows Service identity.
	WinService *WinService `hcl:"winservice,block"`
	// EventStorage is the Primary Instance's event storage policy.
	EventStorage *EventStorage `hcl:"event_storage,block"`
	// Standby is the machine's local redundancy policy, and where a deployed
	// Standby Instance states its own lock, runtime_dir, data_dir, api, winservice, and event_storage.
	Standby *Standby `hcl:"standby,block"`
}

// EventStorage is an instance's event storage policy.
type EventStorage struct {
	EventFabric *EventFabric `hcl:"eventfabric,block"`
}

// EventFabric is an instance's Event Fabric storage adapter policy.
type EventFabric struct {
	Nats *Nats `hcl:"nats,block"`
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

// maxLockWindowsMutex bounds the authored Windows named mutex kernel object name.
const maxLockWindowsMutex = 260

// Lock is a machine's local ownership lock policy.
//
// A machine's primary and standby processes contend for one Windows named mutex,
// authored explicitly so an operator reading the blueprint or descriptor sees the
// exact kernel object name.
type Lock struct {
	// WindowsMutex is the machine-wide kernel object name, which must start with
	// Global\.
	WindowsMutex string `hcl:"windows_mutex"`
}

// Lock returns the machine's authored ownership lock policy, or nil when the
// blueprint does not deploy a standby or states no lock.
func (m Machine) Lock() *Lock {
	if m.Platform == nil || m.Platform.Standby == nil || m.Platform.Standby.Disabled {
		return nil
	}
	return m.Platform.Standby.Lock
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
	// RuntimeDir is the Standby Instance's local runtime directory. It is required
	// when the Standby Instance is deployed and rejected when it is not.
	RuntimeDir string `hcl:"runtime_dir,optional"`
	// DataDir is the Standby Instance's general platform data root. It is
	// required when the Standby Instance is deployed and rejected when it is not.
	DataDir string `hcl:"data_dir,optional"`
	// Lock is the machine's local ownership lock policy. It is required when the
	// Standby Instance is deployed and rejected when it is not.
	Lock *Lock `hcl:"lock,block"`
	// API is the Standby Instance's local API endpoint policy.
	API *API `hcl:"api,block"`
	// WinService is the Standby Instance's Windows Service identity.
	WinService *WinService `hcl:"winservice,block"`
	// EventStorage is the Standby Instance's event storage policy.
	EventStorage *EventStorage `hcl:"event_storage,block"`
}

// Nats is one instance's Event Fabric NATS port policy and JetStream storage location,
// authored so a blueprint reader sees which ports the machine needs open and where
// JetStream files are stored.
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
	// JetStreamStoreDir is the directory where this instance's NATS JetStream server
	// stores its files. Required.
	JetStreamStoreDir string `hcl:"jetstream_store_dir,optional"`
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
	// Site size is checked last, over every site, because it counts what the
	// machines declare. A malformed machine is worth reporting as a malformed
	// machine rather than as a site that came up an instance short because that
	// machine did not parse, and a rule that spans sites, such as a machine name
	// repeated in another one, should not be pre-empted by the size of the first
	// site that happens to be too small.
	for _, site := range p.Sites {
		if err := validateSiteSize(site); err != nil {
			return err
		}
	}
	return nil
}

// minimumSiteMachines and minimumSiteInstances are the smallest site the
// platform supports: two machines, at least one of which deploys a Standby
// Instance.
const (
	minimumSiteMachines  = 2
	minimumSiteInstances = 3
)

// validateSiteSize rejects a site too small for the platform to run on.
//
// The floor is three platform instances across at least two machines. It comes
// from the site journal, which is a JetStream RAFT group: a group of three keeps
// quorum after losing one member, and a group of two needs both members alive,
// which is not redundancy but a second thing that can fail. Below three
// instances the site journal can only be a single copy, and losing the instance
// holding it loses the site's history.
//
// Two machines is required on top of the instance count because three instances
// on one machine survive losing a process but not losing the host, and a host is
// what actually fails. So the minimum is two machines with a standby on one of
// them, which is three instances across two failure domains.
//
// This is a build-time rule rather than a runtime one because a site's shape is
// decided when it is authored. A deployment that cannot be redundant should fail
// where it is written, not at three in the morning when the standby it was
// supposed to have turns out never to have been able to help.
func validateSiteSize(site Site) error {
	if len(site.Machines) < minimumSiteMachines {
		return fmt.Errorf("site %q: %d machine(s); the platform requires at least %d, because a site journal on one host cannot survive losing that host",
			site.Name, len(site.Machines), minimumSiteMachines)
	}
	instances := 0
	for _, machine := range site.Machines {
		if machine.Platform == nil {
			continue
		}
		instances++
		if machine.Platform.Standby != nil && !machine.Platform.Standby.Disabled {
			instances++
		}
	}
	if instances < minimumSiteInstances {
		return fmt.Errorf("site %q: %d platform instance(s); the platform requires at least %d, so deploy a standby on at least one machine: a journal of two members needs both alive and is not redundant",
			site.Name, instances, minimumSiteInstances)
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
	if err := validateInstanceEndpoints(machine, "platform", machine.Platform.API, machine.Platform.EventStorage); err != nil {
		return err
	}
	if err := validateStandbyEndpoints(machine); err != nil {
		return err
	}
	if err := validateRuntimeDirs(machine); err != nil {
		return err
	}
	if err := validateDataDirs(machine); err != nil {
		return err
	}
	if err := validateJetStreamStoreDirs(machine); err != nil {
		return err
	}
	if err := validateMachinePorts(machine); err != nil {
		return err
	}
	if err := validateWinServices(machine); err != nil {
		return err
	}
	return validateLock(machine)
}

// validateInstanceEndpoints checks one instance states both of its endpoint
// policies with usable ports and a JetStream store directory.
func validateInstanceEndpoints(machine Machine, block string, api *API, eventStorage *EventStorage) error {
	if api == nil {
		return fmt.Errorf("machine %q: %s.api block is required", machine.Name, block)
	}
	if err := validatePort(machine.Name, block+".api.local_port", api.LocalPort); err != nil {
		return err
	}
	if eventStorage == nil {
		return fmt.Errorf("machine %q: %s.event_storage block is required", machine.Name, block)
	}
	if eventStorage.EventFabric == nil {
		return fmt.Errorf("machine %q: %s.event_storage.eventfabric block is required", machine.Name, block)
	}
	nats := eventStorage.EventFabric.Nats
	if nats == nil {
		return fmt.Errorf("machine %q: %s.event_storage.eventfabric.nats block is required", machine.Name, block)
	}
	if err := validatePort(machine.Name, block+".event_storage.eventfabric.nats.client_port", nats.ClientPort); err != nil {
		return err
	}
	if err := validatePort(machine.Name, block+".event_storage.eventfabric.nats.cluster_port", nats.ClusterPort); err != nil {
		return err
	}
	if strings.TrimSpace(nats.JetStreamStoreDir) == "" {
		return fmt.Errorf("machine %q: %s.event_storage.eventfabric.nats.jetstream_store_dir is required", machine.Name, block)
	}
	return nil
}

// validateStandbyEndpoints checks a deployed Standby Instance states its own
// endpoints, and that a machine which opts out of a standby states nothing for
// one.
func validateStandbyEndpoints(machine Machine) error {
	standby := machine.Platform.Standby
	if !standby.Disabled {
		return validateInstanceEndpoints(machine, "platform.standby", standby.API, standby.EventStorage)
	}
	if strings.TrimSpace(standby.RuntimeDir) != "" {
		return fmt.Errorf("machine %q: platform.standby.runtime_dir is set but the standby is disabled; remove it or deploy the standby", machine.Name)
	}
	if strings.TrimSpace(standby.DataDir) != "" {
		return fmt.Errorf("machine %q: platform.standby.data_dir is set but the standby is disabled; remove it or deploy the standby", machine.Name)
	}
	if standby.Lock != nil {
		return fmt.Errorf("machine %q: platform.standby.lock is set but the standby is disabled; remove it or deploy the standby", machine.Name)
	}
	if standby.API != nil {
		return fmt.Errorf("machine %q: platform.standby.api is set but the standby is disabled; remove it or deploy the standby", machine.Name)
	}
	if standby.EventStorage != nil {
		return fmt.Errorf("machine %q: platform.standby.event_storage is set but the standby is disabled; remove it or deploy the standby", machine.Name)
	}
	return nil
}

// validateRuntimeDirs checks each deployed instance states its own local runtime
// directory, and that a machine's two instances do not state the same one.
func validateRuntimeDirs(machine Machine) error {
	return validateInstanceDirs(machine, "runtime_dir",
		machine.Platform.RuntimeDir, machine.Platform.Standby.RuntimeDir,
		"the two instances run together and cannot share a runtime directory")
}

// validateDataDirs checks each deployed instance states its own platform data
// root, and that a machine's two instances do not state the same one.
func validateDataDirs(machine Machine) error {
	return validateInstanceDirs(machine, "data_dir",
		machine.Platform.DataDir, machine.Platform.Standby.DataDir,
		"the two instances run together and cannot share a platform data directory")
}

// validateJetStreamStoreDirs checks each deployed instance states its own JetStream
// file store directory, and that a machine's two instances do not state the same one.
func validateJetStreamStoreDirs(machine Machine) error {
	return validateInstanceDirs(machine, "event_storage.eventfabric.nats.jetstream_store_dir",
		machine.JetStreamStoreDir(false), machine.JetStreamStoreDir(true),
		"each instance runs its own Event Fabric server and two servers cannot open the same JetStream store")
}

// validateInstanceDirs checks one per-instance directory attribute: required on
// the primary, required on a deployed standby, and never the same on both.
func validateInstanceDirs(machine Machine, attribute, primaryDir, standbyDir, collision string) error {
	primary := strings.TrimSpace(primaryDir)
	if primary == "" {
		return fmt.Errorf("machine %q: platform.%s is required; every machine deploys a Primary Instance", machine.Name, attribute)
	}
	if machine.Platform.Standby.Disabled {
		return nil
	}
	standby := strings.TrimSpace(standbyDir)
	if standby == "" {
		return fmt.Errorf("machine %q: platform.standby.%s is required when the standby is deployed", machine.Name, attribute)
	}
	if primary == standby {
		return fmt.Errorf("machine %q: platform.%s and platform.standby.%s are both %q; %s", machine.Name, attribute, attribute, primary, collision)
	}
	return nil
}

// validateMachinePorts checks no two listeners on the machine are given the same
// port.
func validateMachinePorts(machine Machine) error {
	type listener struct {
		where string
		port  int
	}
	platform := machine.Platform
	primaryNats := machine.Nats(false)
	listeners := []listener{
		{"platform.api.local_port", platform.API.LocalPort},
		{"platform.event_storage.eventfabric.nats.client_port", primaryNats.ClientPort},
		{"platform.event_storage.eventfabric.nats.cluster_port", primaryNats.ClusterPort},
	}
	if !platform.Standby.Disabled {
		standbyNats := machine.Nats(true)
		listeners = append(listeners,
			listener{"platform.standby.api.local_port", platform.Standby.API.LocalPort},
			listener{"platform.standby.event_storage.eventfabric.nats.client_port", standbyNats.ClientPort},
			listener{"platform.standby.event_storage.eventfabric.nats.cluster_port", standbyNats.ClusterPort},
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

// validateLock checks the ownership lock is authored on a standby machine and
// omitted on a standby-disabled machine.
func validateLock(machine Machine) error {
	standby := machine.Platform.Standby
	if standby.Disabled {
		return nil
	}
	if standby.Lock == nil {
		return fmt.Errorf("machine %q: platform.standby.lock block is required when the standby is deployed", machine.Name)
	}
	name := standby.Lock.WindowsMutex
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("machine %q: platform.standby.lock.windows_mutex is required", machine.Name)
	}
	if name != strings.TrimSpace(name) {
		return fmt.Errorf("machine %q: platform.standby.lock.windows_mutex %q must not have leading or trailing whitespace", machine.Name, name)
	}
	if len(name) > maxLockWindowsMutex {
		return fmt.Errorf("machine %q: platform.standby.lock.windows_mutex %q is longer than %d characters", machine.Name, name, maxLockWindowsMutex)
	}
	if !strings.HasPrefix(name, "Global\\") {
		return fmt.Errorf("machine %q: platform.standby.lock.windows_mutex %q must start with Global\\", machine.Name, name)
	}
	if strings.ContainsAny(name[len("Global\\"):], `\/`) {
		return fmt.Errorf("machine %q: platform.standby.lock.windows_mutex %q must not contain slashes or backslashes after Global\\", machine.Name, name)
	}
	return nil
}

func validatePort(machineName, where string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("machine %q: %s must be in range 1-65535, got %d", machineName, where, port)
	}
	return nil
}

// Nats returns one instance's authored Nats configuration, or nil when that
// instance is not deployed or omits it.
func (m Machine) Nats(standby bool) *Nats {
	if m.Platform == nil {
		return nil
	}
	if !standby {
		if m.Platform.EventStorage == nil || m.Platform.EventStorage.EventFabric == nil {
			return nil
		}
		return m.Platform.EventStorage.EventFabric.Nats
	}
	if m.Platform.Standby == nil || m.Platform.Standby.Disabled || m.Platform.Standby.EventStorage == nil || m.Platform.Standby.EventStorage.EventFabric == nil {
		return nil
	}
	return m.Platform.Standby.EventStorage.EventFabric.Nats
}

// JetStreamStoreDir returns one instance's authored JetStream store directory.
func (m Machine) JetStreamStoreDir(standby bool) string {
	nats := m.Nats(standby)
	if nats == nil {
		return ""
	}
	return strings.TrimSpace(nats.JetStreamStoreDir)
}

// Endpoints resolves one instance's authored endpoint ports, or nil when that
// instance is not deployed. It is the one place resolution asks a machine which
// ports an instance owns, so the primary-under-platform and standby-under-standby
// asymmetry is read in a single place rather than repeated per endpoint.
func (m Machine) Endpoints(standby bool) *Endpoints {
	if m.Platform == nil {
		return nil
	}
	api := m.Platform.API
	nats := m.Nats(standby)
	if standby {
		if m.Platform.Standby == nil || m.Platform.Standby.Disabled {
			return nil
		}
		api = m.Platform.Standby.API
	}
	if api == nil || nats == nil {
		return nil
	}
	return &Endpoints{APILocalPort: api.LocalPort, ClientPort: nats.ClientPort, ClusterPort: nats.ClusterPort}
}

// Endpoints are one instance's authored listener ports.
type Endpoints struct {
	// APILocalPort is bound on loopback; the two nats ports are bound on the
	// machine's ip.
	APILocalPort int
	ClientPort   int
	ClusterPort  int
}

// RuntimeDir returns one instance's authored local runtime directory, or an empty
// string when that instance is not deployed.
func (m Machine) RuntimeDir(standby bool) string {
	if m.Platform == nil {
		return ""
	}
	if !standby {
		return strings.TrimSpace(m.Platform.RuntimeDir)
	}
	if m.Platform.Standby == nil || m.Platform.Standby.Disabled {
		return ""
	}
	return strings.TrimSpace(m.Platform.Standby.RuntimeDir)
}

// DataDir returns one instance's authored JetStream file store directory, or an
// empty string when that instance is not deployed.
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
