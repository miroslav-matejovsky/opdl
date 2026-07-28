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
	// MachineEventsFile is the machine's own append-only event store: the shared
	// file both of its instances append machine-scoped events to.
	//
	// It is the machine's, like the lease, and unlike the lease it is present on
	// every machine. A machine that deploys one instance still has machine facts
	// — which instance owns it, how each activation ended — and they belong in
	// the machine's file rather than in whichever instance stated them.
	MachineEventsFile string `json:"machine_events_file"`
	// Primary is the machine's Primary Instance, always deployed.
	Primary Instance `json:"primary"`
	// Standby is the machine's Standby Instance, present only when the machine
	// deploys one. A runtime that finds it absent has no peer and no failover.
	Standby *Instance `json:"standby,omitempty"`
	// Lease is the machine's resolved local Primary Ownership lease. Present
	// exactly when Standby is.
	Lease *Lease `json:"lease,omitempty"`
}

// HasStandby reports whether this machine deploys a Standby Instance.
func (d Descriptor) HasStandby() bool { return d.Standby != nil }

// Instance returns one role's record. The standby's is the zero record on a
// machine that deploys none, so a caller asking for a peer that does not exist
// reads blank endpoints rather than dereferencing nothing.
func (d Descriptor) Instance(role PlatformInstanceRole) Instance {
	if role == RoleStandby {
		if d.Standby == nil {
			return Instance{}
		}
		return *d.Standby
	}
	return d.Primary
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
	// LagBound is how far a process's projection may fall behind the journal
	// before it stops being promotable, and before an Active process stops
	// serving rather than answering from a stale view.
	//
	// It is on the lease because it bounds a failover: a machine that deploys no
	// standby has no lease, trades ownership with nobody, and therefore has no
	// lag bound either. It is an operational safety bound, not a failover-time
	// SLO.
	LagBound string `json:"lag_bound"`
}

// UnmarshalJSON decodes a descriptor and requires every instance record it
// carries to be complete.
//
// The checks exist because the guarded fields are strings with a usable zero
// value: an omitted api_read_header_timeout would decode as zero, which
// http.Server reads as no limit at all, and an omitted lease timing would decode
// as an ownership record whose grant never expires. Failing here turns a
// truncated or stale descriptor into a startup error instead of a running machine
// with the wrong topology.
//
// The standby is not guarded the same way, because it no longer needs to be. Its
// deployment is stated by the presence of the record rather than by a bool
// inside one, so there is no zero value to mistake for a decision: an absent
// standby carries no endpoints to read and no lease to contend for.
func (d *Descriptor) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	// The machine's shared store is required on every machine, and it has no
	// usable default for the same reason an instance's files have none: an
	// omitted path would decode as the empty string, which is not a file the
	// runtime could fall back to.
	if _, err := requiredField(fields, "machine_events_file"); err != nil {
		return err
	}
	primary, err := requiredField(fields, "primary")
	if err != nil {
		return err
	}
	if err := validateInstanceFields(RolePrimary, primary); err != nil {
		return err
	}
	standby, hasStandby := presentField(fields, "standby")
	if hasStandby {
		if err := validateInstanceFields(RoleStandby, standby); err != nil {
			return err
		}
	}
	if err := validateLeaseField(fields, hasStandby); err != nil {
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

// validateInstanceFields checks one instance record carries everything the
// instance it describes will bind and write.
func validateInstanceFields(role PlatformInstanceRole, raw json.RawMessage) error {
	var instance map[string]json.RawMessage
	if err := json.Unmarshal(raw, &instance); err != nil {
		return fmt.Errorf("deployment descriptor: invalid %s: %w", role, err)
	}
	// The local files are required because the instance opens every one of them at
	// startup and none has a usable default: an omitted path would decode as the
	// empty string, which is not a file the runtime could fall back to.
	for _, field := range []string{"events_file", "state_file", "log_file"} {
		if _, err := requiredField(instance, string(role)+"."+field); err != nil {
			return err
		}
	}
	// The listener timeouts are required for the same reason the address is: the
	// instance binds a listener for its whole lifetime, and a missing timeout
	// would decode as zero, which Go's http.Server reads as "no limit" rather
	// than as an omission.
	for _, field := range []string{"api_read_header_timeout", "api_shutdown_timeout"} {
		name := string(role) + "." + field
		raw, err := requiredField(instance, name)
		if err != nil {
			return err
		}
		if err := checkDurationField(name, raw); err != nil {
			return err
		}
	}
	return nil
}

// checkDurationField verifies a descriptor field holds a valid Go duration
// string, so a truncated or hand-edited descriptor fails at load rather than
// when the value is first needed.
func checkDurationField(name string, raw json.RawMessage) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("deployment descriptor: invalid %s: %w", name, err)
	}
	if _, err := time.ParseDuration(value); err != nil {
		return fmt.Errorf("deployment descriptor: %s %q is not a valid duration: %w", name, value, err)
	}
	return nil
}

// validateLeaseField verifies the lease block matches the standby record: absent
// on a standby-less machine, and present with a file and every timing when a
// standby is deployed. The durations are checked for validity here so a truncated
// or hand-edited descriptor fails at load rather than when ownership is first
// decided.
func validateLeaseField(fields map[string]json.RawMessage, hasStandby bool) error {
	if !hasStandby {
		if _, present := presentField(fields, "lease"); present {
			return fmt.Errorf("deployment descriptor: lease is set but no standby is deployed; omit lease when standby is absent")
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
	for _, field := range []string{"duration", "renewal_interval", "health_check_interval", "failback_stabilization", "lag_bound"} {
		raw, err := requiredField(leaseFields, "lease."+field)
		if err != nil {
			return err
		}
		if err := checkDurationField("lease."+field, raw); err != nil {
			return err
		}
	}
	return nil
}

// requiredField returns fields[name]'s value, or an error naming it. The
// qualified name is used in the error so a reader of a failed startup knows which
// part of the descriptor to look at.
func requiredField(fields map[string]json.RawMessage, name string) (json.RawMessage, error) {
	raw, ok := presentField(fields, name)
	if !ok {
		return nil, fmt.Errorf("deployment descriptor: %s is required", name)
	}
	return raw, nil
}

// presentField looks up a qualified field name, treating both an absent key and
// an explicit null as absent. A field written as null says nothing a missing one
// does not, so the two are one case rather than two the reader has to handle.
func presentField(fields map[string]json.RawMessage, name string) (json.RawMessage, bool) {
	key := name
	if index := strings.LastIndex(name, "."); index >= 0 {
		key = name[index+1:]
	}
	raw, ok := fields[key]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, false
	}
	return raw, true
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

// Instance is one platform instance: what the service running it is called, and
// every endpoint it binds. A record exists only for an instance that is deployed,
// so the runtime cannot mistake a resolved endpoint for one that will never be
// bound.
type Instance struct {
	// Service is the instance's Windows Service identity.
	Service *WinService `json:"service,omitempty"`
	// EventsFile is the instance's own append-only JSON Lines event record. It is
	// opened before anything else the process does and every fact the process
	// states reaches it, including the ones about failing to start.
	EventsFile string `json:"events_file"`
	// StateFile is the instance's own durable state record, carried across
	// restarts and crashes. It holds the instance's epoch counter, which advances
	// by exactly one every time the process starts and every time the instance
	// becomes Active.
	StateFile string `json:"state_file"`
	// LogFile is the instance's own structured application log. The process opens
	// it before anything else it writes and appends one JSON record per line to it
	// for as long as it runs.
	//
	// It is a different record from EventsFile. An event is a fact the platform
	// states and other levels consume; a log record is a diagnostic account of the
	// process that stated it. Neither substitutes for the other, so they are
	// separate files an operator can keep, ship, and delete on different terms.
	LogFile string `json:"log_file"`
	// APIAddress is where this instance serves its local API. Each instance has
	// its own and binds it for its whole lifetime, not only while Active.
	//
	// It is always on loopback: the platform API is machine-local, authored as
	// api.local_port and resolved onto 127.0.0.1, and no instance's API is
	// reachable from the network.
	APIAddress string `json:"api_address"`
	// APIReadHeaderTimeout bounds how long this instance's listener spends
	// reading an HTTP request's headers before closing the connection. It is a Go
	// duration string ("5s"), parsed at startup.
	APIReadHeaderTimeout string `json:"api_read_header_timeout"`
	// APIShutdownTimeout bounds the graceful drain of this instance's listener
	// when it stops serving, whether it is stepping down or the process is
	// leaving.
	APIShutdownTimeout string `json:"api_shutdown_timeout"`
}
