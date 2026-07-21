package deployment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
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
	// Slots is the machine's resolved primary and standby slot decision. Both
	// records are always present.
	Slots Slots `json:"slots"`
	// EventFabric is the resolved Event Fabric topology for this machine.
	EventFabric EventFabric `json:"event_fabric"`
}

// UnmarshalJSON decodes a descriptor and requires every resolved decision it
// depends on to be present in the JSON.
//
// The checks exist because the fields they guard are a bool and a struct, and
// both have a usable zero value. An omitted slots.standby.disabled would decode
// as false and silently deploy redundancy nobody asked for; an omitted
// event_fabric.nats would decode as a machine with no journal to reach. Failing
// here turns a truncated or stale descriptor into a startup error instead of a
// running machine with the wrong topology.
func (d *Descriptor) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	slots, err := requiredField(fields, "slots")
	if err != nil {
		return err
	}
	var slotFields map[string]json.RawMessage
	if err := json.Unmarshal(slots, &slotFields); err != nil {
		return fmt.Errorf("deployment descriptor: invalid slots: %w", err)
	}
	for _, slot := range []string{"primary", "standby"} {
		raw, err := requiredField(slotFields, "slots."+slot)
		if err != nil {
			return err
		}
		var disabled map[string]json.RawMessage
		if err := json.Unmarshal(raw, &disabled); err != nil {
			return fmt.Errorf("deployment descriptor: invalid slots.%s: %w", slot, err)
		}
		if _, err := requiredField(disabled, "slots."+slot+".disabled"); err != nil {
			return err
		}
	}

	fabric, err := requiredField(fields, "event_fabric")
	if err != nil {
		return err
	}
	var fabricFields map[string]json.RawMessage
	if err := json.Unmarshal(fabric, &fabricFields); err != nil {
		return fmt.Errorf("deployment descriptor: invalid event_fabric: %w", err)
	}
	if _, err := requiredField(fabricFields, "event_fabric.nats"); err != nil {
		return err
	}

	type plain Descriptor
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*d = Descriptor(decoded)
	return nil
}

// requiredField returns fields[name]'s value, treating both an absent key and an
// explicit null as missing. The qualified name is used in the error so a reader
// of a failed startup knows which part of the descriptor to look at.
func requiredField(fields map[string]json.RawMessage, name string) (json.RawMessage, error) {
	key := name
	if index := strings.LastIndex(name, "."); index >= 0 {
		key = name[index+1:]
	}
	raw, ok := fields[key]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, fmt.Errorf("deployment descriptor: %s is required", name)
	}
	return raw, nil
}

// Features are the capability switches carried from the project onto a machine.
type Features struct {
	Chaos bool `json:"chaos"`
}

// Slots is a machine's resolved slot topology. Both records are always present
// and non-null, so the runtime never infers a slot policy from an omitted field.
//
// The slots are process roles, not endpoint owners: the primary and standby are
// mutually exclusive owners of the machine's one set of Event Fabric endpoints,
// which is why the resolved NATS topology lives on EventFabric rather than on a
// slot.
type Slots struct {
	Primary Slot `json:"primary"`
	Standby Slot `json:"standby"`
}

// Slot is one process slot's resolved decision.
type Slot struct {
	// Disabled reports that the slot's process is not deployed. It is always
	// false for the primary.
	Disabled bool `json:"disabled"`
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
	// Nats is the machine's one resolved NATS topology, shared by whichever
	// process holds the machine fence.
	Nats EventFabricNats `json:"nats"`
	// Peers are the other Event Fabric members of this machine's site, ordered by
	// machine name. A machine never lists itself, and the fabric spans exactly
	// one site. A single-machine site has no peers and forms a one-member fabric.
	Peers []EventFabricPeer `json:"peers"`
}

// EventFabricNats is the machine's resolved NATS topology: the addresses its own
// server would bind, and the addresses it reaches the site's journal through.
//
// There is exactly one of these per machine. The primary and standby processes
// are mutually exclusive fence owners, so they share it: a standby connects to
// the address the active process is serving on, and promotion rebinds that same
// address rather than moving the site to a second one.
type EventFabricNats struct {
	// ClientAddress is where this machine's server serves the NATS client
	// protocol. It is present on every machine; only a storage node binds it.
	ClientAddress string `json:"client_address"`
	// ClusterAddress is where this machine's server routes to the site's other
	// storage nodes. It is present on every machine; it is bound only when Routes
	// is non-empty.
	ClusterAddress string `json:"cluster_address"`
	// Routes are the cluster addresses of the site's other storage nodes. It is
	// empty for a non-storage machine and for a site with one storage node.
	Routes []string `json:"routes"`
	// Servers are the client addresses this machine reaches the journal through,
	// ordered so a storage node lists its own address first.
	Servers []string `json:"servers"`
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
