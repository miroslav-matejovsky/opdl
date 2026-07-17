package deployment

import (
	"fmt"
	"net"
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
	// EventFabric is the resolved Event Fabric topology for this machine.
	EventFabric EventFabric `json:"event_fabric"`
}

// Features are the capability switches carried from the project onto a machine.
type Features struct {
	Chaos      bool `json:"chaos"`
	Redundancy bool `json:"redundancy"`
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
	return nil
}
