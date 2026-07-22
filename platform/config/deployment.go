package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Descriptor is one machine's deployment definition as the platform consumes it:
// its identity (platform, project, environment, site, machine, machine role, ip),
// the services it hosts, and the project features enabled on it.
//
// It mirrors the builder's deployment descriptor field for field. The platform
// keeps its own copy so the runtime does not depend on the build tool; the
// conformance-tests module checks the two representations stay compatible.
type Descriptor struct {
	// Platform identifies the product line the binary is built from.
	Platform string `json:"platform"`
	// Project, Environment, Site, Machine, MachineProfile place the machine in the
	// topology. MachineProfile is the machine's purpose, such as "sensor-node"; an
	// instance's role is a different axis and is never called role here.
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
	// Features are the project capability switches enabled on the machine.
	Features Features `json:"features"`
	// Instances is this machine's Primary and Standby Instances. Both records are
	// always present.
	Instances Instances `json:"instances"`
	// Lock is the machine's resolved local ownership lock. Present only when the
	// Standby Instance is deployed; omitted on a standby-less machine.
	Lock *Lock `json:"lock,omitempty"`
	// Peers are the platform instances that make up this machine's site,
	// including this machine's own.
	Peers []Peer `json:"peers"`
}

// PlatformInstanceRole is one of the two fixed platform instance roles. The roles are
// decided at build time and never assigned, negotiated, or exchanged at runtime.
type PlatformInstanceRole string

// The two fixed roles a machine's platform instances are built with.
const (
	RolePrimary PlatformInstanceRole = "primary"
	RoleStandby PlatformInstanceRole = "standby"
)

// Role returns the instance role for a standby flag.
func Role(standby bool) PlatformInstanceRole {
	if standby {
		return RoleStandby
	}
	return RolePrimary
}

// Lock is the machine's resolved local ownership lock: the Windows named mutex
// its primary and standby processes contend for, and which exactly one of them
// holds at a time.
//
// Object is fully derived by the builder from an authored lock policy and a digest
// of the machine's whole identity. The runtime trusts it as identity, exactly as
// it trusts the rest of the descriptor, and never composes one of its own.
//
// It is carried in the descriptor rather than derived at runtime because a named
// kernel object is not visible to ordinary tools the way a lock file is. An
// operator reading deployment.json can see which object a machine contends for.
type Lock struct {
	// WindowsMutex is the ownership object's name, including its Global\ prefix.
	WindowsMutex string `json:"windows_mutex"`
}

// UnmarshalJSON decodes a descriptor and requires every resolved decision it
// depends on to be present in the JSON.
//
// The checks exist because the fields they guard are a bool, a string, and a
// struct, and all have a usable zero value. An omitted instances.standby.disabled
// would decode as false and silently deploy redundancy nobody asked for; an
// omitted peers list would decode as a site of one, so a registration would need
// no confirmation but its own; an omitted lock.windows_mutex would decode as an empty
// ownership mutex name, and a machine whose two instances contend for nothing
// has no ownership at all when standby is enabled. Failing here turns a truncated or stale descriptor
// into a startup error instead of a running machine with the wrong topology.
func (d *Descriptor) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	instances, err := requiredField(fields, "instances")
	if err != nil {
		return err
	}
	var instanceFields map[string]json.RawMessage
	if err := json.Unmarshal(instances, &instanceFields); err != nil {
		return fmt.Errorf("deployment descriptor: invalid instances: %w", err)
	}
	for _, role := range []PlatformInstanceRole{RolePrimary, RoleStandby} {
		raw, err := requiredField(instanceFields, "instances."+string(role))
		if err != nil {
			return err
		}
		var instance map[string]json.RawMessage
		if err := json.Unmarshal(raw, &instance); err != nil {
			return fmt.Errorf("deployment descriptor: invalid instances.%s: %w", role, err)
		}
		if _, err := requiredField(instance, "instances."+string(role)+".disabled"); err != nil {
			return err
		}
		var inst struct {
			Disabled bool `json:"disabled"`
		}
		if err := json.Unmarshal(raw, &inst); err != nil {
			return fmt.Errorf("deployment descriptor: invalid instances.%s: %w", role, err)
		}
		if !inst.Disabled {
			if _, err := requiredField(instance, "instances."+string(role)+".data_dir"); err != nil {
				return err
			}
			natsRaw, err := requiredField(instance, "instances."+string(role)+".nats")
			if err != nil {
				return err
			}
			var nats map[string]json.RawMessage
			if err := json.Unmarshal(natsRaw, &nats); err != nil {
				return fmt.Errorf("deployment descriptor: invalid instances.%s.nats: %w", role, err)
			}
			if _, err := requiredField(nats, "instances."+string(role)+".nats.jetstream_store_dir"); err != nil {
				return err
			}
		}
	}

	var standbyPolicy struct {
		Disabled bool `json:"disabled"`
	}
	if err := json.Unmarshal(instanceFields[string(RoleStandby)], &standbyPolicy); err != nil {
		return fmt.Errorf("deployment descriptor: invalid instances.standby: %w", err)
	}
	if err := validateLockField(fields, standbyPolicy.Disabled); err != nil {
		return err
	}

	if _, err := requiredField(fields, "peers"); err != nil {
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

// validateLockField verifies the lock block matches the standby status.
func validateLockField(fields map[string]json.RawMessage, standbyDisabled bool) error {
	if standbyDisabled {
		if _, present := fields["lock"]; present {
			return fmt.Errorf("deployment descriptor: lock is set but instances.standby.disabled is true; omit lock when no standby is deployed")
		}
		return nil
	}
	lockField, err := requiredField(fields, "lock")
	if err != nil {
		return err
	}
	var lockFields map[string]json.RawMessage
	if err := json.Unmarshal(lockField, &lockFields); err != nil {
		return fmt.Errorf("deployment descriptor: invalid lock: %w", err)
	}
	_, err = requiredField(lockFields, "lock.windows_mutex")
	return err
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

// Instances is a machine's two platform instances. Both records are always
// present and non-null, so the runtime never infers an instance's deployment from
// an omitted field.
//
// Each instance is an independent runtime and owns its own endpoints, so
// everything an instance binds is carried on its own record. The machine holds no
// endpoint of its own.
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

// Service returns one instance's Windows Service identity, or nil when that
// instance is not deployed.
func (i Instances) Service(standby bool) *WinService {
	return i.Get(Role(standby)).Service
}

// WinService is one instance's resolved Windows Service identity.
//
// It is a declaration carried for whoever installs the services, not a
// capability. The platform has no Service Control Manager integration and
// installs, starts, and stops nothing. The runtime reads it only to print which
// service should be running this instance, so an operator can match a process to
// an entry in the services list.
type WinService struct {
	// Name is the Windows Service name.
	Name string `json:"name"`
	// DisplayName is the name shown in the services list.
	DisplayName string `json:"display_name"`
	// Description is the optional description shown in the services list.
	Description string `json:"description,omitempty"`
}

// Instance is one platform instance: whether it is deployed, what the service
// running it is called, and every endpoint it binds.
//
// The endpoint fields are present exactly when the instance is deployed, so the
// runtime cannot mistake a resolved endpoint for one that will ever be bound.
type Instance struct {
	// Disabled reports that this instance is not deployed. It is always false for
	// the primary.
	Disabled bool `json:"disabled"`
	// Service is the instance's Windows Service identity, present exactly when the
	// instance is deployed.
	Service *WinService `json:"service,omitempty"`
	// RuntimeDir is the instance's own local runtime directory, holding its status
	// file. It is the instance's rather than the machine's: two independent
	// runtimes writing into one directory would overwrite each other's evidence.
	// It takes no part in the ownership decision.
	RuntimeDir string `json:"runtime_dir,omitempty"`
	// DataDir is the instance's own general platform data root.
	DataDir string `json:"data_dir,omitempty"`
	// APIAddress is where this instance serves its local API. Each instance has
	// its own and binds it for its whole lifetime, not only while Active.
	//
	// It is always on loopback: the platform API is machine-local, authored as
	// api.local_port and resolved onto 127.0.0.1, and no instance's API is
	// reachable from the network.
	APIAddress string `json:"api_address,omitempty"`
	// Nats is this instance's own Event Fabric NATS topology.
	Nats *Nats `json:"nats,omitempty"`
}

// Peer is one platform instance of this machine's site.
//
// The site's members are instances, not machines: each is an independent runtime
// with its own endpoints, and a machine contributes one peer when it deploys only
// a Primary Instance and two when it deploys a Standby Instance as well.
//
// The list includes this machine's own instances. One descriptor is read by both
// instances of a machine, so it carries the site's whole membership and each
// running instance recognises itself by Machine and Role.
//
// A peer carries the Event Fabric addresses only. It has no api_address: the
// platform API is bound on loopback, so another machine's API is not reachable
// and an address stating otherwise would be one no process listens on.
//
// Peers are ordered by machine name, then Primary before Standby, so every
// machine of a site sees the same list. The site is the boundary: instances of
// another site, environment, or project are not peers.
type Peer struct {
	// Site is the peer's site, always equal to this machine's site.
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
// There is one per deployed instance, not one per machine. Each instance runs its
// own server, so on a storage machine that deploys a standby there are two
// cluster members on one host and each routes to the other.
type Nats struct {
	// JetStreamStoreDir is the directory where this instance's NATS JetStream server
	// stores its files.
	JetStreamStoreDir string `json:"jetstream_store_dir,omitempty"`
	// ClientAddress is where this instance's server serves the NATS client
	// protocol. It is present on every instance; only an instance on a storage
	// machine binds it.
	ClientAddress string `json:"client_address"`
	// ClusterAddress is where this instance's server accepts routes from the
	// site's other storage servers. It is present on every instance; it is bound
	// only when Routes is non-empty.
	ClusterAddress string `json:"cluster_address"`
	// Routes are the cluster addresses of the site's other storage servers,
	// including this machine's other instance when the machine stores the journal.
	// It is empty for an instance on a non-storage machine, and for a site with
	// one storage server.
	Routes []string `json:"routes"`
	// Servers are the client addresses this instance reaches the journal through,
	// ordered so an instance on a storage machine lists its own address first.
	Servers []string `json:"servers"`
}
