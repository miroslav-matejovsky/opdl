package deployment

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Descriptor is one machine's deployment definition: everything the platform
// needs to run on that machine, projected from the project blueprint. It is the
// builder's output contract; the platform runtime conforms to it.
type Descriptor struct {
	// Platform identifies the product line the binary is built from. The builder
	// supplies it; it is not part of the project blueprint.
	Platform string `json:"platform"`
	// Project, Environment, Site, Machine, Role place the machine in the topology.
	Project     string `json:"project"`
	Environment string `json:"environment"`
	Site        string `json:"site"`
	Machine     string `json:"machine"`
	Role        string `json:"role"`
	// IP is the machine's network address.
	IP string `json:"ip"`
	// Services are the service groups this machine hosts.
	Services []string `json:"services"`
	// Features are the project capability switches carried onto the machine.
	Features Features `json:"features"`
	// PlatformInstances are the active platform processes configured on this
	// machine, ordered primary then optional secondary. Their endpoints are
	// explicit deployment facts and have no runtime defaults.
	PlatformInstances []PlatformInstance `json:"platform_instances"`
	// Fabric is the resolved platform fabric topology for this machine.
	Fabric Fabric `json:"fabric"`
}

// Features are the capability switches carried from the project onto a machine.
type Features struct {
	Chaos bool `json:"chaos"`
}

const (
	// PlatformInstancePrimary is the required platform process identity.
	PlatformInstancePrimary = "primary"
	// PlatformInstanceSecondary is the optional redundant process identity.
	PlatformInstanceSecondary = "secondary"
)

// PlatformInstance is one active platform process and its explicit endpoints.
type PlatformInstance struct {
	Name                    string `json:"name"`
	APIAddress              string `json:"api_address"`
	FabricClientAddress     string `json:"fabric_client_address"`
	FabricMemberlistAddress string `json:"fabric_memberlist_address"`
}

// Fabric is this machine's resolved view of the platform fabric: the peers it
// forms that fabric with. The descriptor already carries this machine's
// identity, so Fabric contains only the additional topology. The builder derives
// it from the site portion of the project topology, so a machine boots knowing its
// membership without discovering anything at runtime.
//
// It carries the explicit client and membership endpoints needed to bootstrap
// the current adapter. Production code does not derive ports. Adapter-specific
// lifecycle tuning remains outside the descriptor.
type Fabric struct {
	// Peers are the other fabric members of this machine's site, ordered by
	// machine name so every machine derives the same list. A machine never lists
	// itself, and the fabric spans exactly one site: machines of another site,
	// environment, or project form their own fabric.
	//
	// A single-machine site has no peers and forms a one-member fabric.
	Peers []FabricPeer `json:"peers"`
}

// FabricPeer is one other fabric member this machine expects to meet.
type FabricPeer struct {
	// Site is the peer's site. It always equals this machine's site; it is
	// carried so a reader can check that without the rest of the topology.
	Site string `json:"site"`
	// Machine is the peer's machine identity.
	Machine string `json:"machine"`
	// Instance is the peer platform process identity: primary or secondary.
	Instance string `json:"instance"`
	// IP is the address the peer's fabric member is reached on.
	IP string `json:"ip"`
	// FabricClientAddress is the peer's explicit Olric client endpoint.
	FabricClientAddress string `json:"fabric_client_address"`
	// FabricMemberlistAddress is the peer's explicit membership endpoint.
	FabricMemberlistAddress string `json:"fabric_memberlist_address"`
}

// Validate checks a descriptor is complete enough to deploy. resolve calls it
// before compiling, so the builder fails before producing a machine that would
// not boot. It fails fast on the first violation.
func (d Descriptor) Validate() error {
	if strings.TrimSpace(d.Platform) == "" {
		return fmt.Errorf("platform is required")
	}
	if strings.TrimSpace(d.Project) == "" {
		return fmt.Errorf("project is required")
	}
	if strings.TrimSpace(d.Environment) == "" {
		return fmt.Errorf("environment is required")
	}
	if strings.TrimSpace(d.Site) == "" {
		return fmt.Errorf("site is required")
	}
	if strings.TrimSpace(d.Machine) == "" {
		return fmt.Errorf("machine is required")
	}
	if strings.TrimSpace(d.Role) == "" {
		return fmt.Errorf("role is required")
	}
	if net.ParseIP(d.IP) == nil {
		return fmt.Errorf("ip %q is not a valid IP address", d.IP)
	}
	if len(d.Services) == 0 {
		return fmt.Errorf("at least one service is required")
	}
	if err := d.validatePlatformInstances(); err != nil {
		return err
	}
	return d.validateFabric()
}

// validatePlatformInstances checks the resolved local process set and explicit
// endpoints. A descriptor contains exactly primary and optional secondary.
func (d Descriptor) validatePlatformInstances() error {
	if len(d.PlatformInstances) < 1 || len(d.PlatformInstances) > 2 {
		return fmt.Errorf("platform_instances must contain primary and optional secondary, found %d entries", len(d.PlatformInstances))
	}
	used := make(map[string]string)
	for index, instance := range d.PlatformInstances {
		want := PlatformInstancePrimary
		if index == 1 {
			want = PlatformInstanceSecondary
		}
		if instance.Name != want {
			return fmt.Errorf("platform_instances[%d] is %q, expected %q", index, instance.Name, want)
		}
		for _, endpoint := range instanceEndpoints(instance) {
			field, address := endpoint.field, endpoint.address
			if err := validateEndpoint(address); err != nil {
				return fmt.Errorf("platform instance %q %s: %w", instance.Name, field, err)
			}
			if owner, exists := used[address]; exists {
				return fmt.Errorf("platform endpoint %q is used by both %s and %s %s", address, owner, instance.Name, field)
			}
			used[address] = instance.Name + " " + field
		}
	}
	return nil
}

// validateFabric checks the resolved fabric topology is one this machine can
// boot with. A duplicate endpoint or a peer from another site produces a fabric
// that either fails to bind or silently spans a boundary it must not cross.
func (d Descriptor) validateFabric() error {
	validator := fabricValidator{
		descriptor: d,
		machineIPs: map[string]string{d.Machine: d.IP},
		ipMachines: map[string]string{d.IP: d.Machine},
		members:    make(map[string]bool),
		endpoints:  make(map[string]string),
	}
	for _, instance := range d.PlatformInstances {
		validator.endpoints[instance.APIAddress] = d.Machine + "/" + instance.Name + " api"
		validator.endpoints[instance.FabricClientAddress] = d.Machine + "/" + instance.Name + " fabric client"
		validator.endpoints[instance.FabricMemberlistAddress] = d.Machine + "/" + instance.Name + " fabric memberlist"
	}
	for _, peer := range d.Fabric.Peers {
		if err := validator.validate(peer); err != nil {
			return err
		}
	}
	return nil
}

type fabricValidator struct {
	descriptor       Descriptor
	machineIPs       map[string]string
	ipMachines       map[string]string
	members          map[string]bool
	endpoints        map[string]string
	previousMachine  string
	previousInstance string
}

func (v *fabricValidator) validate(peer FabricPeer) error {
	member, err := v.validateIdentity(peer)
	if err != nil {
		return err
	}
	if err := v.validateIP(peer); err != nil {
		return err
	}
	if err := v.validateOrder(peer); err != nil {
		return err
	}
	if err := v.validateEndpoints(member, peer); err != nil {
		return err
	}
	v.members[member] = true
	v.machineIPs[peer.Machine] = peer.IP
	v.ipMachines[peer.IP] = peer.Machine
	v.previousMachine, v.previousInstance = peer.Machine, peer.Instance
	return nil
}

func (v *fabricValidator) validateIdentity(peer FabricPeer) (string, error) {
	if peer.Site != v.descriptor.Site {
		return "", fmt.Errorf("fabric peer %q is in site %q, not this machine's site %q", peer.Machine, peer.Site, v.descriptor.Site)
	}
	if strings.TrimSpace(peer.Machine) == "" {
		return "", fmt.Errorf("fabric peer with empty machine")
	}
	if peer.Machine == v.descriptor.Machine {
		return "", fmt.Errorf("fabric peer %q/%q is this machine itself", peer.Machine, peer.Instance)
	}
	if peer.Instance != PlatformInstancePrimary && peer.Instance != PlatformInstanceSecondary {
		return "", fmt.Errorf("fabric peer %q has invalid instance %q", peer.Machine, peer.Instance)
	}
	member := peer.Machine + "/" + peer.Instance
	if v.members[member] {
		return "", fmt.Errorf("fabric peer %q is duplicated", member)
	}
	return member, nil
}

func (v *fabricValidator) validateIP(peer FabricPeer) error {
	if net.ParseIP(peer.IP) == nil {
		return fmt.Errorf("fabric peer %q: ip %q is not a valid IP address", peer.Machine, peer.IP)
	}
	if machineIP, known := v.machineIPs[peer.Machine]; known && machineIP != peer.IP {
		return fmt.Errorf("fabric peer machine %q uses both ip %q and %q", peer.Machine, machineIP, peer.IP)
	}
	if machine, taken := v.ipMachines[peer.IP]; taken && machine != peer.Machine {
		return fmt.Errorf("fabric peer %q: ip %q is already used by machine %q", peer.Machine, peer.IP, machine)
	}
	return nil
}

func (v *fabricValidator) validateOrder(peer FabricPeer) error {
	if peer.Machine < v.previousMachine || peer.Machine == v.previousMachine && peer.Instance < v.previousInstance {
		return fmt.Errorf("fabric peers are not ordered: %q/%q after %q/%q", peer.Machine, peer.Instance, v.previousMachine, v.previousInstance)
	}
	if peer.Instance == PlatformInstanceSecondary && (v.previousMachine != peer.Machine || v.previousInstance != PlatformInstancePrimary) {
		return fmt.Errorf("fabric peer %q/secondary is not preceded by its primary instance", peer.Machine)
	}
	return nil
}

func (v *fabricValidator) validateEndpoints(member string, peer FabricPeer) error {
	for _, endpoint := range []namedEndpoint{
		{field: "fabric_client_address", address: peer.FabricClientAddress},
		{field: "fabric_memberlist_address", address: peer.FabricMemberlistAddress},
	} {
		if err := validateEndpoint(endpoint.address); err != nil {
			return fmt.Errorf("fabric peer %q %s: %w", member, endpoint.field, err)
		}
		if owner, exists := v.endpoints[endpoint.address]; exists {
			return fmt.Errorf("fabric endpoint %q for %s %s is already used by %s", endpoint.address, member, endpoint.field, owner)
		}
		v.endpoints[endpoint.address] = member + " " + endpoint.field
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

// validateEndpoint checks an explicit descriptor endpoint.
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
