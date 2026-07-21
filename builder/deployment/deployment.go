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
	// Slots is the machine's resolved instance topology: the Primary and Standby
	// Instances, and every endpoint each one binds. Both records are always
	// present.
	Slots Slots `json:"slots"`
	// Fence is the machine's resolved local ownership object.
	Fence Fence `json:"fence"`
	// Peers are the other machines of this machine's site.
	Peers []Peer `json:"peers"`
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

// Peer is one other machine of this machine's site.
//
// Peers are the site's membership, not its wiring. They carry identities and no
// ports, because what a reader wants from this list is machine-level: which
// machines the site consists of. Two things need that and neither is a
// connection. Storage selection picks machines because a machine is the failure
// domain, and placing two journal replicas behind one power supply is not
// redundancy. A registration needs one confirmation per machine, because exactly
// one instance of a machine is Active and it answers for the machine.
//
// The wiring is per-instance and resolved onto the slots. Nothing derives a
// connection from this list.
//
// The builder fills it from the site portion of the project topology, so a
// machine boots knowing its membership without discovering anything at runtime.
// A machine never lists itself, and the site is the boundary: machines of another
// site, environment, or project are not peers and form their own fabric. A
// single-machine site has no peers.
//
// Peers are ordered by machine name, so every machine derives the same list
// however the blueprint was authored.
type Peer struct {
	// Site is the peer's site. It always equals this machine's site; it is
	// carried so a reader can check that without the rest of the topology.
	Site string `json:"site"`
	// Machine is the peer's machine identity.
	Machine string `json:"machine"`
	// IP is the address the peer's machine is reached on.
	IP string `json:"ip"`
}

// EventFabricNats is one instance's resolved NATS topology: the addresses its own
// server binds, and the addresses it reaches the site's journal through.
//
// There is one of these per deployed instance, not one per machine. Each instance
// runs its own server, so on a storage machine that runs a standby there are two
// cluster members on one host and each routes to the other. That is why the
// record lives on the slot: an instance's endpoints are its own, and no instance
// inherits or takes over another's.
type EventFabricNats struct {
	// ClientAddress is where this instance's server serves the client protocol,
	// derived from the machine ip and the instance's authored client port. It is
	// present on every instance; only one on a storage machine binds it.
	ClientAddress string `json:"client_address"`
	// ClusterAddress is where this instance's server routes to the site's other
	// storage servers, derived from the machine ip and the instance's authored
	// cluster port. It is present on every instance; it is bound only when Routes
	// is non-empty.
	ClusterAddress string `json:"cluster_address"`
	// Routes are the cluster addresses of the site's other storage servers,
	// including the other instance of this machine when both store the journal. It
	// is empty for an instance on a non-storage machine, and for a site with one
	// storage server, which has no peer to route to.
	Routes []string `json:"routes"`
	// Servers are the client addresses this instance reaches the journal through,
	// ordered so an instance on a storage machine lists its own address first.
	Servers []string `json:"servers"`
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
	if err := d.validateSlotEndpoints(); err != nil {
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

// validateSlotEndpoints checks each deployed instance carries the endpoints it
// binds, that a disabled standby carries none, and that no two listeners on the
// machine were resolved onto the same address.
//
// The addresses are checked against each other rather than only for validity
// because both instances run at once on one host. Two listeners resolved to one
// address is a machine where the second process cannot start, and every address
// here is derived from the same machine ip, so a repeated port is a repeated
// address.
func (d Descriptor) validateSlotEndpoints() error {
	if err := d.validateInstanceEndpoints("slots.primary", d.Slots.Primary); err != nil {
		return err
	}
	if d.Slots.Standby.Disabled {
		if d.Slots.Standby.APIAddress != "" {
			return fmt.Errorf("slots.standby.api_address is set but the standby is disabled")
		}
		if d.Slots.Standby.Nats != nil {
			return fmt.Errorf("slots.standby.nats is set but the standby is disabled")
		}
		return nil
	}
	if err := d.validateInstanceEndpoints("slots.standby", d.Slots.Standby); err != nil {
		return err
	}
	bound := []struct {
		where   string
		address string
	}{
		{"slots.primary.api_address", d.Slots.Primary.APIAddress},
		{"slots.primary.nats.client_address", d.Slots.Primary.Nats.ClientAddress},
		{"slots.primary.nats.cluster_address", d.Slots.Primary.Nats.ClusterAddress},
		{"slots.standby.api_address", d.Slots.Standby.APIAddress},
		{"slots.standby.nats.client_address", d.Slots.Standby.Nats.ClientAddress},
		{"slots.standby.nats.cluster_address", d.Slots.Standby.Nats.ClusterAddress},
	}
	taken := make(map[string]string, len(bound))
	for _, listener := range bound {
		if owner, used := taken[listener.address]; used {
			return fmt.Errorf("%s and %s are both %q; the two instances run together and cannot share a listener",
				owner, listener.where, listener.address)
		}
		taken[listener.address] = listener.where
	}
	return nil
}

// validateInstanceEndpoints checks one deployed instance's own addresses.
func (d Descriptor) validateInstanceEndpoints(prefix string, slot Slot) error {
	if strings.TrimSpace(slot.APIAddress) == "" {
		return fmt.Errorf("%s.api_address is required", prefix)
	}
	if err := validateAddress(slot.APIAddress); err != nil {
		return fmt.Errorf("%s.api_address: %w", prefix, err)
	}
	if slot.Nats == nil {
		return fmt.Errorf("%s.nats is required", prefix)
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
	if err := d.validateNats("slots.primary.nats", d.Slots.Primary.Nats); err != nil {
		return err
	}
	if d.Slots.Standby.Disabled {
		return nil
	}
	return d.validateNats("slots.standby.nats", d.Slots.Standby.Nats)
}

// validateNats checks one instance's resolved NATS topology matches the site's
// storage selection. The resolver derives all of it, so a violation here is a
// resolver defect rather than an authoring mistake: it means an instance would
// boot pointing at the wrong journal, binding a route it must not bind, or with
// no server to connect to at all.
func (d Descriptor) validateNats(prefix string, nats *EventFabricNats) error {
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
	return d.validateStorageTopology(prefix, nats)
}

// validateStorageTopology checks one instance's listener ownership against the
// storage selection the descriptor implies.
//
// The selection is by machine, and the descriptor carries its site's machines, so
// whether this machine stores the journal needs nothing the runtime does not also
// have. How many storage *servers* the site has does not: that depends on which
// peers deploy a standby, which is each peer's own decision and appears nowhere
// here. The peer list stays machine-level deliberately, so rather than teach it
// about instances the check uses the relation between the two lists.
//
// For an instance on a storage machine, every other storage server contributes
// exactly one route and one server entry, and the instance contributes its own
// client address to the servers only. So routes are always one shorter than
// servers, whatever the site's shape, and a resolver that dropped or doubled a
// server breaks the relation.
func (d Descriptor) validateStorageTopology(prefix string, nats *EventFabricNats) error {
	if !slices.Contains(StorageMachines(d.siteMachines()), d.Machine) {
		if len(nats.Routes) > 0 {
			return fmt.Errorf("%s.routes: an instance on a machine that does not store the journal has no cluster to route to", prefix)
		}
		if slices.Contains(nats.Servers, nats.ClientAddress) {
			return fmt.Errorf("%s.servers: lists this instance's own client address %q, which it does not bind", prefix, nats.ClientAddress)
		}
		return nil
	}
	// A storage server answers its own clients. Listing its own address first
	// keeps its client on the local server while that server is up, so an
	// instance does not route its own traffic through a peer.
	if nats.Servers[0] != nats.ClientAddress {
		return fmt.Errorf("%s.servers: an instance on a storage machine must list its own client address %q first, got %q",
			prefix, nats.ClientAddress, nats.Servers[0])
	}
	if len(nats.Routes) != len(nats.Servers)-1 {
		return fmt.Errorf("%s: %d route(s) for %d server(s); an instance routes to every storage server but its own",
			prefix, len(nats.Routes), len(nats.Servers))
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
