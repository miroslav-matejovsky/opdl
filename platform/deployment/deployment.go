package deployment

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Descriptor is one machine's deployment definition as the platform consumes it:
// its identity (platform, project, environment, site, machine, role, ip), the
// services it hosts, and the project features enabled on it.
//
// It mirrors the builder's deployment descriptor field for field. The platform
// keeps its own copy so the runtime does not depend on the build tool; the
// conformance-tests module checks the two representations stay compatible.
type Descriptor struct {
	// Platform identifies the product line the binary is built from.
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
	// Features are the project capability switches enabled on the machine.
	Features Features `json:"features"`
	// Slots is the machine's primary and optional standby slot definition. It is always present.
	Slots Slots `json:"slots"`
	// EventFabric is the resolved Event Fabric topology for this machine.
	EventFabric EventFabric `json:"event_fabric"`
}

// UnmarshalJSON decodes a descriptor and requires its resolved slots policy
// to be explicit. Without the presence checks, omitted JSON fields would decode
// and silently turn an incomplete descriptor into a primary-only opt-out.
func (d *Descriptor) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	slots, ok := fields["slots"]
	if !ok || bytes.Equal(bytes.TrimSpace(slots), []byte("null")) {
		return fmt.Errorf("deployment descriptor: slots is required")
	}
	var policy map[string]json.RawMessage
	if err := json.Unmarshal(slots, &policy); err != nil {
		return fmt.Errorf("deployment descriptor: invalid slots policy: %w", err)
	}
	primary, ok := policy["primary"]
	if !ok || bytes.Equal(bytes.TrimSpace(primary), []byte("null")) {
		return fmt.Errorf("deployment descriptor: slots.primary is required")
	}

	type plain Descriptor
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*d = Descriptor(decoded)
	return nil
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
// derives it from the project topology and stages it here, so the platform boots
// knowing its membership and discovers nothing at runtime.
//
// It is transport-neutral: identities and addresses only, with no ports, adapter
// names, or protocol settings. The Event Fabric adapter derives what it needs
// from these addresses, which is what lets the transport be replaced without
// changing the deployment contract.
type EventFabric struct {
	// Peers are the other Event Fabric members of this machine's site, ordered by
	// machine name. A machine never lists itself, and the fabric spans exactly
	// one site. A single-machine site has no peers and forms a one-member fabric.
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
	// Site is the peer's site, always equal to this machine's site.
	Site string `json:"site"`
	// Machine is the peer's machine identity.
	Machine string `json:"machine"`
	// IP is the address the peer's Event Fabric member is reached on.
	IP string `json:"ip"`
}
