package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Descriptor is one machine's deployment definition as the platform consumes it:
// its identity (platform, project, environment, site, machine, machine role, ip),
// and the services it hosts.
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
	// Instances is this machine's Primary and Standby Instances. Both records are
	// always present.
	Instances Instances `json:"instances"`
	// Lease is the machine's resolved local Primary Ownership lease. Present only
	// when the Standby Instance is deployed; omitted on a standby-less machine.
	Lease *Lease `json:"lease,omitempty"`
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

// Lease is the machine's resolved local Primary Ownership lease: the shared
// machine-wide file its primary and standby processes record ownership in, and
// the timings that govern how ownership is held, renewed, and turned over.
//
// Every field is derived by the builder from the machine's authored standby
// policy. The runtime trusts them as identity, exactly as it trusts the rest of
// the descriptor, and never composes a lease of its own.
//
// It is carried in the descriptor rather than derived at runtime because the file
// path and the failover timings are deployment policy an operator must be able to
// read in deployment.json. The durations are Go duration strings ("15s", "5s");
// the platform parses them at startup.
type Lease struct {
	// File is the machine-wide lease file both instances read and write, an
	// absolute path on a local filesystem.
	File string `json:"file"`
	// Duration is how long a granted lease is valid without renewal.
	Duration string `json:"duration"`
	// RenewalInterval is how often the owner extends the lease.
	RenewalInterval string `json:"renewal_interval"`
	// HealthCheckInterval is how often a Passive instance evaluates promotion and
	// polls its peer's health.
	HealthCheckInterval string `json:"health_check_interval"`
	// FailbackStabilization is how long a returning Primary must be continuously
	// healthy before an Active Standby hands ownership back to it.
	FailbackStabilization string `json:"failback_stabilization"`
}

// UnmarshalJSON decodes a descriptor and requires every resolved decision it
// depends on to be present in the JSON.
//
// The checks exist because the fields they guard are a bool, a string, and a
// struct, and all have a usable zero value. An omitted instances.standby.disabled
// would decode as false and silently deploy redundancy nobody asked for; an
// omitted peers list would decode as a site of one, so a registration would need
// no confirmation but its own; an omitted lock.windows_mutex would decode as an empty
// ownership record, and a machine whose two instances coordinate through nothing
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
		if err := validateInstanceFields(role, raw); err != nil {
			return err
		}
	}

	var standbyPolicy struct {
		Disabled bool `json:"disabled"`
	}
	if err := json.Unmarshal(instanceFields[string(RoleStandby)], &standbyPolicy); err != nil {
		return fmt.Errorf("deployment descriptor: invalid instances.standby: %w", err)
	}
	if err := validateLeaseField(fields, standbyPolicy.Disabled); err != nil {
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

// validateInstanceFields checks one instance record states the policy a reader
// must not infer: whether it is deployed at all, and, when it is, where it
// writes and what it coordinates through.
func validateInstanceFields(role PlatformInstanceRole, raw json.RawMessage) error {
	var instance map[string]json.RawMessage
	if err := json.Unmarshal(raw, &instance); err != nil {
		return fmt.Errorf("deployment descriptor: invalid instances.%s: %w", role, err)
	}
	if _, err := requiredField(instance, "instances."+string(role)+".disabled"); err != nil {
		return err
	}
	var policy struct {
		Disabled bool `json:"disabled"`
	}
	if err := json.Unmarshal(raw, &policy); err != nil {
		return fmt.Errorf("deployment descriptor: invalid instances.%s: %w", role, err)
	}
	// An instance that is not deployed carries nothing else, and nothing else is
	// required of it.
	if policy.Disabled {
		return nil
	}
	if _, err := requiredField(instance, "instances."+string(role)+".data_dir"); err != nil {
		return err
	}
	return validateInstanceNatsField(role, instance["nats"])
}

// validateInstanceNatsField checks a deployed instance's event storage record.
//
// The record is optional: a machine that authored no event storage deploys an
// instance with no Event Fabric, which binds its API and serves no domain
// operation. When it is present it must be usable, because an instance that
// thinks it has a journal and cannot open one is worse than one that knows it
// has none.
func validateInstanceNatsField(role PlatformInstanceRole, raw json.RawMessage) error {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var nats map[string]json.RawMessage
	if err := json.Unmarshal(raw, &nats); err != nil {
		return fmt.Errorf("deployment descriptor: invalid instances.%s.nats: %w", role, err)
	}
	_, err := requiredField(nats, "instances."+string(role)+".nats.jetstream_store_dir")
	return err
}

// validateLeaseField verifies the lease block matches the standby status: absent
// on a standby-less machine, and present with a file and every timing when a
// standby is deployed. The durations are checked for validity here so a truncated
// or hand-edited descriptor fails at load rather than when ownership is first
// decided.
func validateLeaseField(fields map[string]json.RawMessage, standbyDisabled bool) error {
	if standbyDisabled {
		if _, present := fields["lease"]; present {
			return fmt.Errorf("deployment descriptor: lease is set but instances.standby.disabled is true; omit lease when no standby is deployed")
		}
		return nil
	}
	leaseField, err := requiredField(fields, "lease")
	if err != nil {
		return err
	}
	var leaseFields map[string]json.RawMessage
	if err := json.Unmarshal(leaseField, &leaseFields); err != nil {
		return fmt.Errorf("deployment descriptor: invalid lease: %w", err)
	}
	if _, err := requiredField(leaseFields, "lease.file"); err != nil {
		return err
	}
	for _, field := range []string{"duration", "renewal_interval", "health_check_interval", "failback_stabilization"} {
		raw, err := requiredField(leaseFields, "lease."+field)
		if err != nil {
			return err
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("deployment descriptor: invalid lease.%s: %w", field, err)
		}
		if _, err := time.ParseDuration(value); err != nil {
			return fmt.Errorf("deployment descriptor: lease.%s %q is not a valid duration: %w", field, value, err)
		}
	}
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
	// DataDir is the instance's own general platform data root.
	DataDir string `json:"data_dir,omitempty"`
	// APIAddress is where this instance serves its local API. Each instance has
	// its own and binds it for its whole lifetime, not only while Active.
	//
	// It is always on loopback: the platform API is machine-local, authored as
	// api.local_port and resolved onto 127.0.0.1, and no instance's API is
	// reachable from the network.
	APIAddress string `json:"api_address,omitempty"`
}
