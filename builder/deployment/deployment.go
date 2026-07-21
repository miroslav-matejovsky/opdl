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
//
// A machine deploys one or two platform instances, and each is an independent
// runtime with its own endpoints. The descriptor is still per machine because one
// binary is built per machine and both instances run from it, so it states the
// machine's identity once and every instance's endpoints separately.
type Descriptor struct {
	// Platform identifies the product line the binary is built from. The builder
	// supplies it; it is not part of the project blueprint.
	Platform string `json:"platform"`
	// Project, Environment, Site, Machine, MachineProfile place the machine in the
	// topology. MachineProfile is the machine's purpose, such as "sensor-node".
	Project        string `json:"project"`
	Environment    string `json:"environment"`
	Site           string `json:"site"`
	Machine        string `json:"machine"`
	MachineProfile string `json:"machine_profile"`
	// IP is the machine's network address. Both of its instances are reached on
	// it, on their own ports.
	IP string `json:"ip"`
	// Services are the service groups this machine hosts.
	Services []string `json:"services"`
	// Features are the project capability switches carried onto the machine.
	Features Features `json:"features"`
	// Instances is this machine's Primary and Standby Instances. Both records are
	// always present.
	Instances Instances `json:"instances"`
	// Lock is the machine's resolved local ownership lock. Present only when the
	// Standby Instance is deployed; omitted on a standby-less machine.
	Lock *Lock `json:"lock,omitempty"`
	// Peers are the platform instances that make up this machine's site.
	Peers []Peer `json:"peers"`
}

// PlatformInstanceRole is one of the two fixed platform instance roles.
//
// The roles are decided at build time and never assigned, negotiated, or
// exchanged at runtime. An instance that takes over does not become the Primary
// Instance; it operates Active until ownership returns.
type PlatformInstanceRole string

// The two fixed roles a machine's platform instances are built with.
const (
	RolePrimary PlatformInstanceRole = "primary"
	RoleStandby PlatformInstanceRole = "standby"
)

// Lock is the machine's resolved local ownership lock: the Windows named mutex
// its two instances contend for, and which exactly one of them holds at a time.
//
// It is recorded here rather than derived at runtime because a named kernel object
// is not visible to ordinary tools the way a lock file is. An operator reading
// deployment.json can see exactly which object a machine will contend for.
//
// It is the machine's, not an instance's: it is the one thing the two instances
// share, and sharing it is what makes exactly one of them Active. It is present
// only when the Standby Instance is deployed (Instances.Standby.Disabled is false);
// on a standby-less machine, there is no lock and no contention.
type Lock struct {
	// WindowsMutex is the machine-wide kernel object name, including its Global\
	// namespace prefix.
	WindowsMutex string `json:"windows_mutex"`
}

// Features are the capability switches carried from the project onto a machine.
type Features struct {
	Chaos bool `json:"chaos"`
}

// Instances is a machine's two platform instances. Both records are always
// present and non-null, so a reader never infers an instance's deployment from an
// omitted field.
//
// Each instance is an independent runtime and owns its own endpoints, so
// everything an instance binds is resolved onto its own record. The machine holds
// no endpoint of its own.
type Instances struct {
	Primary Instance `json:"primary"`
	Standby Instance `json:"standby"`
}

// Get returns one instance by role.
func (i Instances) Get(role PlatformInstanceRole) Instance {
	if role == RoleStandby {
		return i.Standby
	}
	return i.Primary
}

// Instance is one platform instance: whether it is deployed, what the service
// running it is called, and every endpoint it binds.
//
// The endpoint fields are present exactly when the instance is deployed. A
// disabled standby carries nothing but Disabled, so a reader cannot mistake a
// resolved endpoint for one that will ever be bound.
type Instance struct {
	// Disabled reports that this instance is not deployed. It is always false for
	// the primary: a machine with no Primary Instance would deploy nothing that
	// can serve.
	Disabled bool `json:"disabled"`
	// Service is the instance's Windows Service identity. It is carried for
	// whoever installs the services; the runtime does not read it and the platform
	// manages no services.
	Service *WinService `json:"service,omitempty"`
	// RuntimeDir is the instance's own local runtime directory, holding its status
	// file. It is the instance's rather than the machine's: two independent
	// runtimes writing into one directory would overwrite each other's evidence,
	// and nothing would report it. It takes no part in the ownership decision.
	RuntimeDir string `json:"runtime_dir,omitempty"`
	// DataDir is the instance's own JetStream file store directory.
	//
	// It is present on every deployed instance; only an instance on a storage
	// machine opens it, the same way ClusterAddress is present everywhere and
	// bound only where there are routes. It is the instance's rather than the
	// machine's because each instance runs its own Event Fabric server, and two
	// servers cannot open one store.
	DataDir string `json:"data_dir,omitempty"`
	// APIAddress is where this instance serves its local API: 127.0.0.1 joined to
	// the instance's authored api local_port.
	//
	// It is always on loopback. The platform API is machine-local, so this address
	// is never derived from the machine ip and no instance's API is reachable from
	// the network. Validate enforces that.
	//
	// Each instance has its own, and binds it for its whole lifetime rather than
	// only while Active. A caller that needs the Active instance resolves which
	// one that is; it does not get there by an address that changes owner.
	APIAddress string `json:"api_address,omitempty"`
	// Nats is this instance's own Event Fabric NATS topology.
	Nats *Nats `json:"nats,omitempty"`
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

// Peer is one platform instance of a site.
//
// The site's members are instances, not machines: each instance is an independent
// runtime with its own endpoints, and a machine contributes one peer when it
// deploys only a Primary Instance and two when it deploys a Standby Instance as
// well.
//
// The list includes this machine's own instances. The descriptor is per machine
// and both instances read the same copy, so it states the site's whole membership
// once and each running instance recognises itself by Machine and Role. A list
// that excluded the reader could not be written once for two readers.
//
// A peer carries its addresses because reaching it is what its identity is for.
// The builder fills them from the site portion of the project topology, so an
// instance boots knowing every member without discovering anything at runtime.
//
// Those addresses are the Event Fabric's only. A peer has no api_address: the
// platform API is bound on loopback, so another machine's API is not reachable
// and an address stating otherwise would be one no process listens on.
//
// Peers are ordered by machine name, then Primary before Standby, so every
// machine of a site derives the same list however the blueprint was authored. The
// site is the boundary: instances of another site, environment, or project are
// not peers and form their own fabric.
type Peer struct {
	// Site is the peer's site. It always equals this machine's site; it is
	// carried so a reader can check that without the rest of the topology.
	Site string `json:"site"`
	// Machine is the machine the peer instance runs on.
	Machine string `json:"machine"`
	// Role is which of the machine's two instances this peer is.
	Role PlatformInstanceRole `json:"role"`
	// IP is the address the peer's machine is reached on. Two peers on one machine
	// share it and differ by port.
	IP string `json:"ip"`
	// Nats are the peer instance's Event Fabric addresses.
	Nats PeerNats `json:"nats"`
}

// PeerNats are one peer instance's Event Fabric addresses.
type PeerNats struct {
	// ClientAddress is where the peer's server serves the NATS client protocol.
	ClientAddress string `json:"client_address"`
	// ClusterAddress is where the peer's server accepts routes from the site's
	// other storage servers.
	ClusterAddress string `json:"cluster_address"`
}

// Nats is one instance's resolved NATS topology: the addresses its own server
// binds, and the addresses it reaches the site's journal through.
//
// There is one of these per deployed instance, not one per machine. Each instance
// runs its own server, so on a storage machine that deploys a standby there are
// two cluster members on one host and each routes to the other.
type Nats struct {
	// ClientAddress is where this instance's server serves the client protocol,
	// derived from the machine ip and the instance's authored client port. It is
	// present on every instance; only an instance on a storage machine binds it.
	ClientAddress string `json:"client_address"`
	// ClusterAddress is where this instance's server accepts routes from the
	// site's other storage servers, derived from the machine ip and the instance's
	// authored cluster port. It is present on every instance; it is bound only
	// when Routes is non-empty.
	ClusterAddress string `json:"cluster_address"`
	// Routes are the cluster addresses of the site's other storage servers,
	// including this machine's other instance when the machine stores the journal.
	// It is empty for an instance on a non-storage machine, and for a site with
	// one storage server, which has no peer to route to.
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
	if strings.TrimSpace(d.MachineProfile) == "" {
		return fmt.Errorf("machine profile is required")
	}
	if net.ParseIP(d.IP) == nil {
		return fmt.Errorf("ip %q is not a valid IP address", d.IP)
	}
	if len(d.Services) == 0 {
		return fmt.Errorf("at least one service is required")
	}
	if d.Instances.Standby.Disabled {
		if d.Lock != nil {
			return fmt.Errorf("lock is set but instances.standby.disabled is true; omit lock when no standby is deployed")
		}
	} else {
		if d.Lock == nil {
			return fmt.Errorf("lock is required when instances.standby.disabled is false")
		}
		if strings.TrimSpace(d.Lock.WindowsMutex) == "" {
			return fmt.Errorf("lock.windows_mutex is required")
		}
	}
	if d.Instances.Primary.Disabled {
		return fmt.Errorf("instances.primary.disabled: a machine must deploy a Primary Instance")
	}
	if err := d.validateServices(); err != nil {
		return err
	}
	if err := d.validateEndpoints(); err != nil {
		return err
	}
	if err := d.validatePeers(); err != nil {
		return err
	}
	return d.validateNats()
}

// validateServices checks each instance's Windows Service identity is present
// exactly when that instance is deployed, and that the two differ.
//
// The two instances run on one host, so identical names are the one service
// collision Windows cannot refuse at install time for us.
func (d Descriptor) validateServices() error {
	if d.Instances.Primary.Service == nil {
		return fmt.Errorf("instances.primary.service is required")
	}
	if strings.TrimSpace(d.Instances.Primary.Service.Name) == "" {
		return fmt.Errorf("instances.primary.service.name is required")
	}
	if d.Instances.Standby.Disabled {
		if d.Instances.Standby.Service != nil {
			return fmt.Errorf("instances.standby.service is set but the standby is disabled")
		}
		return nil
	}
	if d.Instances.Standby.Service == nil {
		return fmt.Errorf("instances.standby.service is required when the standby is deployed")
	}
	if strings.TrimSpace(d.Instances.Standby.Service.Name) == "" {
		return fmt.Errorf("instances.standby.service.name is required")
	}
	if d.Instances.Primary.Service.Name == d.Instances.Standby.Service.Name {
		return fmt.Errorf("the primary and standby instances share service name %q", d.Instances.Primary.Service.Name)
	}
	return nil
}

// validateEndpoints checks each deployed instance carries the endpoints it binds,
// that a disabled standby carries none, and that no two listeners on the machine
// were resolved onto the same address.
//
// The addresses are checked against each other rather than only for validity
// because both instances run at once on one host. Two listeners resolved to one
// address is a machine where the second process cannot start, and every address
// here derives from the same machine ip, so a repeated port is a repeated
// address.
func (d Descriptor) validateEndpoints() error {
	if err := validateInstanceEndpoints("instances.primary", d.Instances.Primary); err != nil {
		return err
	}
	if d.Instances.Standby.Disabled {
		if d.Instances.Standby.APIAddress != "" {
			return fmt.Errorf("instances.standby.api_address is set but the standby is disabled")
		}
		if d.Instances.Standby.RuntimeDir != "" {
			return fmt.Errorf("instances.standby.runtime_dir is set but the standby is disabled")
		}
		if d.Instances.Standby.DataDir != "" {
			return fmt.Errorf("instances.standby.data_dir is set but the standby is disabled")
		}
		if d.Instances.Standby.Nats != nil {
			return fmt.Errorf("instances.standby.nats is set but the standby is disabled")
		}
		return nil
	}
	if err := validateInstanceEndpoints("instances.standby", d.Instances.Standby); err != nil {
		return err
	}
	// The two instances write their status files into their own directories. One
	// directory for both would have them overwriting each other's evidence, and
	// unlike a shared listener nothing would report it.
	if d.Instances.Primary.RuntimeDir == d.Instances.Standby.RuntimeDir {
		return fmt.Errorf("instances.primary.runtime_dir and instances.standby.runtime_dir are both %q; the two instances run together and cannot share a runtime directory",
			d.Instances.Primary.RuntimeDir)
	}
	// Each instance runs its own Event Fabric server, and two NATS servers cannot
	// open one JetStream store. Unlike the runtime directory this does report
	// itself at startup, but it reports as a store that will not open rather than
	// as a descriptor naming one directory twice.
	if d.Instances.Primary.DataDir == d.Instances.Standby.DataDir {
		return fmt.Errorf("instances.primary.data_dir and instances.standby.data_dir are both %q; each instance runs its own Event Fabric server and two servers cannot open the same JetStream store",
			d.Instances.Primary.DataDir)
	}
	bound := []struct {
		where   string
		address string
	}{
		{"instances.primary.api_address", d.Instances.Primary.APIAddress},
		{"instances.primary.nats.client_address", d.Instances.Primary.Nats.ClientAddress},
		{"instances.primary.nats.cluster_address", d.Instances.Primary.Nats.ClusterAddress},
		{"instances.standby.api_address", d.Instances.Standby.APIAddress},
		{"instances.standby.nats.client_address", d.Instances.Standby.Nats.ClientAddress},
		{"instances.standby.nats.cluster_address", d.Instances.Standby.Nats.ClusterAddress},
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

// validateInstanceEndpoints checks one deployed instance's own addresses and its
// runtime directory.
func validateInstanceEndpoints(prefix string, instance Instance) error {
	if err := requireAddress(prefix+".api_address", instance.APIAddress); err != nil {
		return err
	}
	// The platform API is machine-local by design, authored as api.local_port and
	// resolved onto 127.0.0.1. Checking it here is what makes that a property of
	// the deployment contract rather than a convention the resolver happens to
	// follow: a descriptor that would expose an instance's API to the network
	// cannot be built.
	if err := requireLoopback(prefix+".api_address", instance.APIAddress); err != nil {
		return err
	}
	if strings.TrimSpace(instance.RuntimeDir) == "" {
		return fmt.Errorf("%s.runtime_dir is required", prefix)
	}
	if strings.TrimSpace(instance.DataDir) == "" {
		return fmt.Errorf("%s.data_dir is required", prefix)
	}
	if instance.Nats == nil {
		return fmt.Errorf("%s.nats is required", prefix)
	}
	if err := requireAddress(prefix+".nats.client_address", instance.Nats.ClientAddress); err != nil {
		return err
	}
	return requireAddress(prefix+".nats.cluster_address", instance.Nats.ClusterAddress)
}

// validatePeers checks the site's membership is a usable instance list: every
// peer is in this machine's site, no instance appears twice, this machine's own
// deployed instances are present, and the order is the one every machine of the
// site derives.
func (d Descriptor) validatePeers() error {
	seen := make(map[string]bool, len(d.Peers))
	addresses := make(map[string]string, len(d.Peers)*3)
	previous := ""
	for _, peer := range d.Peers {
		if peer.Site != d.Site {
			return fmt.Errorf("peer %s is in site %q, not this machine's site %q", peer, peer.Site, d.Site)
		}
		if strings.TrimSpace(peer.Machine) == "" {
			return fmt.Errorf("peer with empty machine")
		}
		if peer.Role != RolePrimary && peer.Role != RoleStandby {
			return fmt.Errorf("peer on machine %q has role %q, want %q or %q", peer.Machine, peer.Role, RolePrimary, RoleStandby)
		}
		key := peer.key()
		if seen[key] {
			return fmt.Errorf("peer %s is listed twice", peer)
		}
		seen[key] = true
		if net.ParseIP(peer.IP) == nil {
			return fmt.Errorf("peer %s: ip %q is not a valid IP address", peer, peer.IP)
		}
		if err := requireAddress(fmt.Sprintf("peer %s: nats.client_address", peer), peer.NatsClient()); err != nil {
			return err
		}
		if err := requireAddress(fmt.Sprintf("peer %s: nats.cluster_address", peer), peer.NatsCluster()); err != nil {
			return err
		}
		// Every Event Fabric listener in the site is distinct. Two peers on one
		// machine differ by port, and two machines differ by ip, so a repeat means
		// an instance would fail to bind or would silently answer for another.
		for _, listener := range []struct{ what, address string }{
			{"nats.client_address", peer.NatsClient()},
			{"nats.cluster_address", peer.NatsCluster()},
		} {
			if owner, used := addresses[listener.address]; used {
				return fmt.Errorf("peer %s: %s %q is already used by %s", peer, listener.what, listener.address, owner)
			}
			addresses[listener.address] = fmt.Sprintf("%s %s", peer, listener.what)
		}
		if key < previous {
			return fmt.Errorf("peers are not ordered by machine then role: %s after %s", key, previous)
		}
		previous = key
	}
	return d.validateSelfIsPeer()
}

// validateSelfIsPeer checks this machine's own deployed instances appear in the
// membership.
//
// The list is the site's whole membership rather than the reader's counterparts,
// because one descriptor is read by both of a machine's instances. An instance
// missing itself would be a member no peer expects to hear from, which is a
// registration that can never be confirmed.
func (d Descriptor) validateSelfIsPeer() error {
	for _, role := range []PlatformInstanceRole{RolePrimary, RoleStandby} {
		if d.Instances.Get(role).Disabled {
			continue
		}
		if !slices.ContainsFunc(d.Peers, func(p Peer) bool {
			return p.Machine == d.Machine && p.Role == role
		}) {
			return fmt.Errorf("peers do not include this machine's own %s instance", role)
		}
	}
	return nil
}

// NatsClient is the peer's NATS client address.
func (p Peer) NatsClient() string { return p.Nats.ClientAddress }

// NatsCluster is the peer's NATS cluster address.
func (p Peer) NatsCluster() string { return p.Nats.ClusterAddress }

// key orders and identifies a peer: machine first, then Primary before Standby.
func (p Peer) key() string { return p.Machine + "\x00" + string(p.Role) }

// String names a peer the way an operator would: the machine and which of its
// instances.
func (p Peer) String() string { return fmt.Sprintf("%s/%s", p.Machine, p.Role) }

// validateNats checks each deployed instance's resolved NATS topology matches the
// site's storage selection. The resolver derives all of it, so a violation here
// is a resolver defect rather than an authoring mistake: it means an instance
// would boot pointing at the wrong journal, binding a route it must not bind, or
// with no server to connect to at all.
func (d Descriptor) validateNats() error {
	if err := d.validateInstanceNats("instances.primary", d.Instances.Primary); err != nil {
		return err
	}
	if d.Instances.Standby.Disabled {
		return nil
	}
	return d.validateInstanceNats("instances.standby", d.Instances.Standby)
}

func (d Descriptor) validateInstanceNats(prefix string, instance Instance) error {
	nats := instance.Nats
	if len(nats.Servers) == 0 {
		return fmt.Errorf("%s.nats.servers: at least one server is required", prefix)
	}
	for _, server := range nats.Servers {
		if err := validateAddress(server); err != nil {
			return fmt.Errorf("%s.nats.servers: %w", prefix, err)
		}
	}
	if duplicate, found := firstDuplicate(nats.Servers); found {
		return fmt.Errorf("%s.nats.servers: %q is listed twice", prefix, duplicate)
	}
	for _, route := range nats.Routes {
		if err := validateAddress(route); err != nil {
			return fmt.Errorf("%s.nats.routes: %w", prefix, err)
		}
		if route == nats.ClusterAddress {
			return fmt.Errorf("%s.nats.routes: %q is this instance itself", prefix, route)
		}
	}
	if duplicate, found := firstDuplicate(nats.Routes); found {
		return fmt.Errorf("%s.nats.routes: %q is listed twice", prefix, duplicate)
	}
	return d.validateStorageTopology(prefix, nats)
}

// validateStorageTopology checks one instance's listener ownership against the
// storage selection the descriptor implies.
//
// Storage is selected by machine, not by instance. A machine is the failure
// domain: two of a site's journal replicas behind one power supply is not
// redundancy, so a machine that stores the journal stores one copy however many
// instances it deploys. Both of its instances run a server, and those two servers
// route to each other.
func (d Descriptor) validateStorageTopology(prefix string, nats *Nats) error {
	storage := StorageMachines(d.siteMachines())
	if !slices.Contains(storage, d.Machine) {
		if len(nats.Routes) > 0 {
			return fmt.Errorf("%s.nats.routes: an instance on a machine that does not store the journal has no cluster to route to", prefix)
		}
		if slices.Contains(nats.Servers, nats.ClientAddress) {
			return fmt.Errorf("%s.nats.servers: lists this instance's own client address %q, which it does not bind", prefix, nats.ClientAddress)
		}
		return nil
	}
	// A storage server answers its own clients. Listing its own address first
	// keeps its client on the local server while that server is up, so an instance
	// does not route its own traffic through a peer.
	if nats.Servers[0] != nats.ClientAddress {
		return fmt.Errorf("%s.nats.servers: an instance on a storage machine must list its own client address %q first, got %q",
			prefix, nats.ClientAddress, nats.Servers[0])
	}
	// Every other storage server contributes exactly one route and one server
	// entry, and this instance contributes its own client address to the servers
	// only. The relation holds whatever the site's shape, so a resolver that
	// dropped or doubled a server breaks it.
	if len(nats.Routes) != len(nats.Servers)-1 {
		return fmt.Errorf("%s.nats: %d route(s) for %d server(s); an instance routes to every storage server but its own",
			prefix, len(nats.Routes), len(nats.Servers))
	}
	return nil
}

// siteMachines returns every machine of this machine's site, including itself.
// Peers are instances, so a machine that deploys two of them contributes two
// peers and one machine.
func (d Descriptor) siteMachines() []string {
	machines := make([]string, 0, len(d.Peers)+1)
	machines = append(machines, d.Machine)
	for _, peer := range d.Peers {
		machines = append(machines, peer.Machine)
	}
	slices.Sort(machines)
	return slices.Compact(machines)
}

// StorageMachines returns the machines of a site that host the site journal,
// sorted by machine name: one for a site smaller than three machines, the first
// three otherwise.
//
// The rule is deterministic by name so every machine of the site derives the
// same set without coordinating. Three rather than two is what keeps the
// journal's metadata group able to hold quorum when one member is lost.
//
// The unit is the machine because that is the failure domain. Every instance a
// storage machine deploys runs a server, so a site can have more storage servers
// than storage machines, and replica placement must keep a machine's servers from
// holding more than one copy.
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

// requireAddress checks a required host:port field is present and usable.
func requireAddress(where, addr string) error {
	if strings.TrimSpace(addr) == "" {
		return fmt.Errorf("%s is required", where)
	}
	if err := validateAddress(addr); err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	return nil
}

// requireLoopback checks a host:port field is bound on the loopback interface, so
// nothing outside the machine can reach it.
func requireLoopback(where, addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%s: %q must be host:port: %w", where, addr, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("%s %q is not on the loopback interface; the platform API is machine-local and is never exposed to the network", where, addr)
	}
	return nil
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
