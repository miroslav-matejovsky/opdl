package deployment

import (
	"fmt"
	"net"
	"slices"
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
	// Slots is the machine's resolved primary and standby slot decision. Both
	// records are always present.
	Slots Slots `json:"slots"`
	// Fence is the machine's resolved local ownership object.
	Fence Fence `json:"fence"`
	// EventFabric is the resolved Event Fabric topology for this machine.
	EventFabric EventFabric `json:"event_fabric"`
}

// Fence is the machine's resolved local ownership object: the Windows named mutex
// its primary and standby processes contend for, and which exactly one of them
// holds at a time.
//
// Object is fully derived. The blueprint authors only a namespace; the builder
// joins it with a digest of the machine's whole identity, so two machines can
// never be given the same object and a deployment cannot state one directly.
//
// It is recorded here rather than derived at runtime because a named kernel object
// is not visible to ordinary tools the way a lock file is. An operator reading
// deployment.json can see exactly which object a machine will contend for.
type Fence struct {
	// Object is the ownership object's name, without a kernel namespace prefix.
	// The platform places it in Global\ itself.
	Object string `json:"object"`
}

// Features are the capability switches carried from the project onto a machine.
type Features struct {
	Chaos bool `json:"chaos"`
}

// Slots is a machine's resolved instance topology. Both records are always
// present and non-null, so a reader never infers a slot policy from an omitted
// field.
//
// Each slot is an independent runtime and owns its own endpoints, so everything
// an instance binds is resolved onto its slot: its API address and its own NATS
// server's topology. The machine holds no endpoint of its own.
type Slots struct {
	Primary Slot `json:"primary"`
	Standby Slot `json:"standby"`
}

// Slot is one instance's resolved decision: whether it is deployed, what the
// service running it is called, and every endpoint it binds.
//
// The endpoint fields are present exactly when the instance is deployed. A
// disabled standby carries nothing but Disabled, so a reader cannot mistake a
// resolved endpoint for one that will ever be bound.
type Slot struct {
	// Disabled reports that the slot's process is not deployed. It is always
	// false for the primary: a machine with no primary process would deploy
	// nothing that can serve.
	Disabled bool `json:"disabled"`
	// Service is the instance's Windows Service identity. It is carried for
	// whoever installs the services; the runtime does not read it and the platform
	// manages no services.
	Service *WinService `json:"service,omitempty"`
	// APIAddress is where this instance serves its local API, derived from the
	// machine ip and the instance's authored api port.
	//
	// Each instance has its own, and binds it for its whole lifetime rather than
	// only while Active. A caller that needs the Active instance resolves which
	// one that is; it does not get there by an address that changes owner.
	APIAddress string `json:"api_address,omitempty"`
	// Nats is this instance's own Event Fabric NATS topology.
	Nats *EventFabricNats `json:"nats,omitempty"`
}

// Instance returns one slot by instance role.
func (s Slots) Instance(standby bool) Slot {
	if standby {
		return s.Standby
	}
	return s.Primary
}

// WinService is one instance's resolved Windows Service identity.
//
// It is a declaration, not a capability. The platform has no Service Control
// Manager integration and installs, starts, and stops nothing. These names exist
// so the two fixed instance roles are recognizable in a services list and named
// identically on every machine, and so the intent to run under the Service
// Control Manager is visible in the package rather than only in a plan.
type WinService struct {
	// Name is the Windows Service name.
	Name string `json:"name"`
	// DisplayName is the name shown in the services list. The builder fills it
	// from Name when a blueprint does not author one.
	DisplayName string `json:"display_name"`
	// Description is the optional description shown in the services list.
	Description string `json:"description,omitempty"`
}

// EventFabric is this machine's resolved view of the site's Event Fabric: the
// peers it forms that fabric with. The descriptor already carries this machine's
// identity, so EventFabric contains only the additional topology. The builder
// derives it from the site portion of the project topology, so a machine boots
// knowing its membership without discovering anything at runtime.
//
// It is deliberately transport-neutral: it carries identities and addresses, and
// no ports, adapter names, or protocol settings. The Event Fabric adapter derives
// what it needs from these addresses, which is what keeps the transport
// replaceable without changing what the builder produces.
type EventFabric struct {
	// Nats is the machine's one resolved NATS topology, shared by whichever
	// instance holds Primary Ownership.
	Nats EventFabricNats `json:"nats"`
	// Peers are the other Event Fabric members of this machine's site, ordered by
	// machine name so every machine derives the same list. A machine never lists
	// itself, and the fabric spans exactly one site: machines of another site,
	// environment, or project form their own fabric.
	//
	// A single-machine site has no peers and forms a one-member fabric.
	Peers []EventFabricPeer `json:"peers"`
}

// EventFabricNats is the machine's resolved NATS topology: the addresses its own
// server would bind, and the addresses it reaches the site's journal through.
//
// There is exactly one of these per machine. The primary and standby processes
// are mutually exclusive holders of Primary Ownership, so they share it: a
// transfer does not change the address other machines were told to connect to.
type EventFabricNats struct {
	// ClientAddress is where this machine's server serves the client protocol,
	// derived from the machine ip and the authored client port. It is present on
	// every machine; only a storage node binds it.
	ClientAddress string `json:"client_address"`
	// ClusterAddress is where this machine's server routes to the site's other
	// storage nodes, derived from the machine ip and the authored cluster port.
	// It is present on every machine; it is bound only when Routes is non-empty.
	ClusterAddress string `json:"cluster_address"`
	// Routes are the cluster addresses of the site's other storage nodes. It is
	// empty for a non-storage machine and for a site with one storage node, which
	// has no peer server to route to.
	Routes []string `json:"routes"`
	// Servers are the client addresses this machine reaches the journal through,
	// ordered so a storage node lists its own address first.
	Servers []string `json:"servers"`
}

// EventFabricPeer is one other Event Fabric member this machine expects to meet.
type EventFabricPeer struct {
	// Site is the peer's site. It always equals this machine's site; it is
	// carried so a reader can check that without the rest of the topology.
	Site string `json:"site"`
	// Machine is the peer's machine identity.
	Machine string `json:"machine"`
	// IP is the address the peer's Event Fabric member is reached on.
	IP string `json:"ip"`
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
	if strings.TrimSpace(d.Fence.Object) == "" {
		return fmt.Errorf("fence object is required")
	}
	if err := d.validateSlotServices(); err != nil {
		return err
	}
	return d.validateEventFabric()
}

// validateSlotServices checks each instance's Windows Service identity is
// present exactly when that instance is deployed, and that the two differ.
//
// The two instances run on one host, so identical names are the one service
// collision Windows cannot refuse at install time for us.
func (d Descriptor) validateSlotServices() error {
	if d.Slots.Primary.Service == nil {
		return fmt.Errorf("slots.primary.service is required")
	}
	if strings.TrimSpace(d.Slots.Primary.Service.Name) == "" {
		return fmt.Errorf("slots.primary.service.name is required")
	}
	if d.Slots.Standby.Disabled {
		if d.Slots.Standby.Service != nil {
			return fmt.Errorf("slots.standby.service is set but the standby is disabled")
		}
		return nil
	}
	if d.Slots.Standby.Service == nil {
		return fmt.Errorf("slots.standby.service is required when the standby is deployed")
	}
	if strings.TrimSpace(d.Slots.Standby.Service.Name) == "" {
		return fmt.Errorf("slots.standby.service.name is required")
	}
	if d.Slots.Primary.Service.Name == d.Slots.Standby.Service.Name {
		return fmt.Errorf("the primary and standby instances share service name %q", d.Slots.Primary.Service.Name)
	}
	return nil
}

// validateEventFabric checks the resolved Event Fabric topology is one this
// machine can boot with. The rules exist because fixed addresses are derived
// from it: a duplicate address or a peer from another site produces a fabric
// that either fails to bind or silently spans a boundary it must not cross.
func (d Descriptor) validateEventFabric() error {
	machines := map[string]bool{d.Machine: true}
	ips := map[string]bool{d.IP: true}
	previous := ""
	for _, peer := range d.EventFabric.Peers {
		if peer.Site != d.Site {
			return fmt.Errorf("event fabric peer %q is in site %q, not this machine's site %q", peer.Machine, peer.Site, d.Site)
		}
		if strings.TrimSpace(peer.Machine) == "" {
			return fmt.Errorf("event fabric peer with empty machine")
		}
		if machines[peer.Machine] {
			return fmt.Errorf("event fabric peer %q is duplicated or is this machine itself", peer.Machine)
		}
		if net.ParseIP(peer.IP) == nil {
			return fmt.Errorf("event fabric peer %q: ip %q is not a valid IP address", peer.Machine, peer.IP)
		}
		if ips[peer.IP] {
			return fmt.Errorf("event fabric peer %q: ip %q is already used by another member", peer.Machine, peer.IP)
		}
		if peer.Machine < previous {
			return fmt.Errorf("event fabric peers are not ordered by machine: %q after %q", peer.Machine, previous)
		}
		machines[peer.Machine] = true
		ips[peer.IP] = true
		previous = peer.Machine
	}
	if d.Slots.Primary.Disabled {
		return fmt.Errorf("slots.primary.disabled: a machine must deploy a primary process")
	}
	return d.validateNats()
}

// validateNats checks the resolved NATS topology matches the site's storage
// selection. The resolver derives all of it, so a violation here is a resolver
// defect rather than an authoring mistake: it means a machine would boot
// pointing at the wrong journal, binding a route it must not bind, or with no
// server to connect to at all.
func (d Descriptor) validateNats() error {
	const prefix = "event_fabric.nats"
	nats := d.EventFabric.Nats
	if strings.TrimSpace(nats.ClientAddress) == "" {
		return fmt.Errorf("%s.client_address is required", prefix)
	}
	if err := validateAddress(nats.ClientAddress); err != nil {
		return fmt.Errorf("%s.client_address: %w", prefix, err)
	}
	if strings.TrimSpace(nats.ClusterAddress) == "" {
		return fmt.Errorf("%s.cluster_address is required", prefix)
	}
	if err := validateAddress(nats.ClusterAddress); err != nil {
		return fmt.Errorf("%s.cluster_address: %w", prefix, err)
	}
	if len(nats.Servers) == 0 {
		return fmt.Errorf("%s.servers: at least one server is required", prefix)
	}
	for _, server := range nats.Servers {
		if err := validateAddress(server); err != nil {
			return fmt.Errorf("%s.servers: %w", prefix, err)
		}
	}
	if duplicate, found := firstDuplicate(nats.Servers); found {
		return fmt.Errorf("%s.servers: %q is listed twice", prefix, duplicate)
	}
	for _, route := range nats.Routes {
		if err := validateAddress(route); err != nil {
			return fmt.Errorf("%s.routes: %w", prefix, err)
		}
		if route == nats.ClusterAddress {
			return fmt.Errorf("%s.routes: %q is this machine itself", prefix, route)
		}
	}
	if duplicate, found := firstDuplicate(nats.Routes); found {
		return fmt.Errorf("%s.routes: %q is listed twice", prefix, duplicate)
	}
	return d.validateStorageTopology(prefix)
}

// validateStorageTopology checks the machine's listener ownership against the
// storage selection the descriptor implies. The selection is derived from the
// site membership the descriptor already carries, so the check needs nothing the
// runtime does not also have.
func (d Descriptor) validateStorageTopology(prefix string) error {
	nats := d.EventFabric.Nats
	storage := StorageMachines(d.siteMachines())
	hostsStorage := slices.Contains(storage, d.Machine)

	if hostsStorage {
		// A storage node answers its own clients. Listing its own address first
		// keeps its client on the local server while that server is up, so a
		// instance that becomes Active does not route its own traffic through a peer.
		if nats.Servers[0] != nats.ClientAddress {
			return fmt.Errorf("%s.servers: a storage machine must list its own client address %q first, got %q",
				prefix, nats.ClientAddress, nats.Servers[0])
		}
	} else if len(nats.Routes) > 0 {
		return fmt.Errorf("%s.routes: a machine that does not store the journal has no cluster to route to", prefix)
	}
	// A site with one storage node has no peer server to route to, so binding a
	// cluster listener there would open a port nothing can connect to.
	if len(storage) < 2 && len(nats.Routes) > 0 {
		return fmt.Errorf("%s.routes: a site with %d storage machine(s) has no routes", prefix, len(storage))
	}
	return nil
}

// siteMachines returns every machine of this machine's site, including itself.
func (d Descriptor) siteMachines() []string {
	machines := make([]string, 0, len(d.EventFabric.Peers)+1)
	machines = append(machines, d.Machine)
	for _, peer := range d.EventFabric.Peers {
		machines = append(machines, peer.Machine)
	}
	return machines
}

// StorageMachines returns the machines of a site that host the site journal,
// sorted by machine name: one for a site smaller than three machines, the first
// three otherwise.
//
// The rule is deterministic by name so every machine of the site derives the
// same set without coordinating. Three rather than two is what keeps the
// journal's metadata group able to hold quorum when one member is lost.
func StorageMachines(machines []string) []string {
	sorted := slices.Clone(machines)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)
	switch {
	case len(sorted) == 0:
		return nil
	case len(sorted) <= smallSiteMax:
		return sorted[:1]
	default:
		return sorted[:3]
	}
}

// smallSiteMax is the largest site that runs one storage machine. A site with
// three or more machines runs three.
const smallSiteMax = 2

// firstDuplicate returns the first repeated value in values.
func firstDuplicate(values []string) (string, bool) {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if seen[value] {
			return value, true
		}
		seen[value] = true
	}
	return "", false
}

func validateAddress(addr string) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%q must be host:port: %w", addr, err)
	}
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("%q has no host", addr)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("%q: port is not a number", addr)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("%q: port %d out of range 1-65535", addr, port)
	}
	return nil
}
