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
	// Slots is the machine's primary and optional standby slot definition. It is always present.
	Slots Slots `json:"slots"`
	// EventFabric is the resolved Event Fabric topology for this machine.
	EventFabric EventFabric `json:"event_fabric"`
}

// Features are the capability switches carried from the project onto a machine.
type Features struct {
	Chaos bool `json:"chaos"`
}

// Slots is a machine's resolved slot topology: the primary slot and optional standby slot.
// It is always present in a generated descriptor, so a reader never has to infer
// the default.
type Slots struct {
	Primary Slot  `json:"primary"`
	Standby *Slot `json:"standby,omitempty"`
}

// Slot represents a process slot on the machine.
type Slot struct {
	EventFabric SlotEventFabric `json:"event_fabric"`
}

// SlotEventFabric holds the slot-specific Event Fabric adapter configurations.
type SlotEventFabric struct {
	Nats EventFabricNats `json:"nats"`
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
	// Peers are the other Event Fabric members of this machine's site, ordered by
	// machine name so every machine derives the same list. A machine never lists
	// itself, and the fabric spans exactly one site: machines of another site,
	// environment, or project form their own fabric.
	//
	// A single-machine site has no peers and forms a one-member fabric.
	Peers []EventFabricPeer `json:"peers"`
}

// EventFabricNats is this machine's explicit NATS configuration resolved from
// the blueprint. It carries the exact addresses where the machine serves NATS
// and connects to its peers.
type EventFabricNats struct {
	ClientAddress  string   `json:"client_address"`
	ClusterAddress string   `json:"cluster_address"`
	MonitorAddress string   `json:"monitor_address"`
	Routes         []string `json:"routes"`
	Servers        []string `json:"servers"`
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
	return d.validateEventFabric()
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
	if err := validateSlotNats("slots.primary.event_fabric.nats", d.Slots.Primary.EventFabric.Nats); err != nil {
		return err
	}
	if d.Slots.Standby != nil {
		if err := validateSlotNats("slots.standby.event_fabric.nats", d.Slots.Standby.EventFabric.Nats); err != nil {
			return err
		}
	}
	return nil
}

func validateSlotNats(prefix string, nats EventFabricNats) error {
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
	if strings.TrimSpace(nats.MonitorAddress) == "" {
		return fmt.Errorf("%s.monitor_address is required", prefix)
	}
	if err := validateAddress(nats.MonitorAddress); err != nil {
		return fmt.Errorf("%s.monitor_address: %w", prefix, err)
	}
	if len(nats.Servers) == 0 {
		return fmt.Errorf("%s.servers: at least one server is required", prefix)
	}
	for _, route := range nats.Routes {
		if err := validateAddress(route); err != nil {
			return fmt.Errorf("%s.routes: %w", prefix, err)
		}
	}
	for _, server := range nats.Servers {
		if err := validateAddress(server); err != nil {
			return fmt.Errorf("%s.servers: %w", prefix, err)
		}
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
