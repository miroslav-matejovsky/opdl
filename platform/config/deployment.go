package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
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
	// Services are the services this machine hosts, each with the probe policy
	// the platform runs against it. Both of the machine's instances probe every
	// one of them, in every ownership state.
	//
	// This is the local half of the health contract, and the only half carrying
	// an endpoint. A probe reaches a service on this machine's own ip, so no
	// other machine's descriptor has any use for these ports and paths.
	Services []Service `json:"services"`
	// SiteServices is every deployed service unit at this machine's site,
	// including this machine's own, in one order every machine of the site was
	// built with.
	//
	// It is the remote half, and it carries no endpoint. An instance needs to
	// know which units exist, who is expected to report on each, and how long a
	// report stays fresh, so that a unit nobody has reported on reads as Unknown
	// rather than as absent. It never needs to reach one: only the machine
	// hosting a service probes it.
	SiteServices []SiteService `json:"site_services"`
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

// Service is one service this machine hosts and how the platform learns whether
// it is up.
//
// The name is unique within the machine; the site tells two copies of one
// service apart by the machine each runs on. Role is metadata rather than a
// probe input: the platform probes a master and a slave the same way and
// reports what it found.
type Service struct {
	// Name is the service identifier, unique within this machine.
	Name string `json:"name"`
	// Role is the part this machine's copy plays: "master" or "slave".
	Role string `json:"role"`
	// HealthCheck is the probe policy for this service.
	HealthCheck HealthCheck `json:"health_check"`
}

// HealthCheck is the probe policy for one service: what to ask, how often, how
// long to wait, and how much failure to tolerate before the service counts as
// down.
//
// The durations are Go duration strings, parsed once at startup like the lease
// timings. They are validated when the descriptor decodes, so a truncated or
// hand-edited descriptor fails at load rather than when the first probe is due.
type HealthCheck struct {
	// Type is the kind of probe. "http" is the only kind today.
	Type string `json:"type"`
	// Port is the port on this machine the probe connects to: the service's own
	// listener, never one the platform binds. Two services may share it when they
	// answer on different paths.
	Port int `json:"port"`
	// Path is the request target as authored: a path and an optional query, with
	// no scheme, host, or fragment. Where the probe connects is this machine's ip
	// and the port above.
	Path string `json:"path"`
	// Interval is how often the probe runs.
	Interval string `json:"interval"`
	// Timeout bounds one probe attempt, and is shorter than Interval.
	Timeout string `json:"timeout"`
	// Retries is how many consecutive failed attempts mark the service down.
	Retries int `json:"retries"`
}

// SiteService is one deployed service unit anywhere at this site, as every
// machine of the site was told about it.
//
// Every descriptor at one site carries the same list in the same order, which is
// what lets two instances that received the same reports reduce them to the same
// view. It is the static half of that view: it says what exists and who should
// be reporting, which is what makes a service nobody has reported on Unknown
// rather than missing.
type SiteService struct {
	// Machine is the machine hosting this unit, and MachineProfile is that
	// machine's purpose. With Service they key the unit within the site.
	Machine        string `json:"machine"`
	MachineProfile string `json:"machine_profile"`
	// Service is the service name as its machine authored it.
	Service string `json:"service"`
	// ServiceRole is the part that copy plays: "master" or "slave".
	ServiceRole string `json:"service_role"`
	// ObserverRoles are the platform instance roles expected to report on this
	// unit: "primary", plus "standby" when the hosting machine deploys one.
	//
	// An expected observer that goes silent is a fact about the platform rather
	// than about the service, and this is what keeps the two from looking alike.
	ObserverRoles []string `json:"observer_roles"`
	// FreshFor is how long one observer's report on this unit stays current,
	// measured by the receiver from when it arrived rather than from any clock
	// the sender stamped.
	FreshFor string `json:"fresh_for"`
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
	// The health contract is checked on the decoded values rather than on raw
	// fields, because what has to hold about it is relationships between typed
	// values — a timeout shorter than its interval, an inventory that lists this
	// machine's own services — and not merely that keys are present.
	if err := Descriptor(decoded).validateHealth(); err != nil {
		return err
	}
	*d = Descriptor(decoded)
	return nil
}

// probeTypeHTTP is the one probe kind a descriptor may carry.
const probeTypeHTTP = "http"

// serviceRoles are the parts a machine's copy of a service may play.
var serviceRoles = []string{"master", "slave"}

// observerRoles are the platform instance roles that may be expected to report
// on a unit, in the order a descriptor lists them.
var observerRoles = []string{string(RolePrimary), string(RoleStandby)}

type unitKey struct{ machine, service, role string }

// validateHealth checks both halves of the health contract are complete and
// agree about this machine.
//
// It runs at decode time, with the rest of the descriptor's guards, because the
// runtime is about to schedule real requests against a live service from these
// values. A probe policy with a zero interval is not something to fall back
// from: it is a descriptor that lost one.
func (d Descriptor) validateHealth() error {
	if len(d.Services) == 0 {
		return fmt.Errorf("deployment descriptor: services is required")
	}
	if len(d.SiteServices) == 0 {
		return fmt.Errorf("deployment descriptor: site_services is required; it carries this machine's own services too")
	}
	seenRoles := make(map[string]map[string]bool, len(d.Services))
	hosted := make(map[unitKey]bool, len(d.Services))
	for _, service := range d.Services {
		name := strings.TrimSpace(service.Name)
		if name == "" {
			return fmt.Errorf("deployment descriptor: a service has no name")
		}
		if service.Name != name {
			return fmt.Errorf("deployment descriptor: services[%s].name %q must not have leading or trailing whitespace", name, service.Name)
		}
		if !slices.Contains(serviceRoles, service.Role) {
			return fmt.Errorf("deployment descriptor: services[%s].role %q is not a known role; the known roles are %s",
				name, service.Role, strings.Join(serviceRoles, ", "))
		}
		roles, ok := seenRoles[name]
		if !ok {
			roles = make(map[string]bool, 2)
			seenRoles[name] = roles
		}
		if roles[service.Role] {
			return fmt.Errorf("deployment descriptor: service %q with role %q is hosted more than once", name, service.Role)
		}
		roles[service.Role] = true
		hosted[unitKey{strings.TrimSpace(d.Machine), name, service.Role}] = true
		if err := service.HealthCheck.validate(fmt.Sprintf("services[%s].health_check", name)); err != nil {
			return err
		}
	}
	if err := d.validateServiceProbeEndpoints(); err != nil {
		return err
	}
	return d.validateSiteServices(hosted)
}

// validate checks one probe policy is complete and runnable.
func (h HealthCheck) validate(where string) error {
	if h.Type != probeTypeHTTP {
		return fmt.Errorf("deployment descriptor: %s.type %q is not a known probe; the known probes are %s", where, h.Type, probeTypeHTTP)
	}
	if h.Port < 1 || h.Port > 65535 {
		return fmt.Errorf("deployment descriptor: %s.port %d is out of range 1-65535", where, h.Port)
	}
	if err := validateHealthRequestTarget(where+".path", h.Path); err != nil {
		return err
	}
	interval, err := validatePositiveDescriptorDuration(where+".interval", h.Interval)
	if err != nil {
		return err
	}
	timeout, err := validatePositiveDescriptorDuration(where+".timeout", h.Timeout)
	if err != nil {
		return err
	}
	if timeout >= interval {
		return fmt.Errorf("deployment descriptor: %s.timeout %s must be shorter than interval %s", where, h.Timeout, h.Interval)
	}
	if h.Retries < 1 {
		return fmt.Errorf("deployment descriptor: %s.retries must be at least 1, got %d", where, h.Retries)
	}
	return nil
}

// validateHealthRequestTarget checks an HTTP probe carries only the path and
// optional query sent to the service. The scheme and host come from the probe
// type and machine descriptor.
func validateHealthRequestTarget(where, target string) error {
	prefix := "deployment descriptor: "
	if strings.TrimSpace(target) == "" {
		return fmt.Errorf("%s%s is required", prefix, where)
	}
	if target != strings.TrimSpace(target) {
		return fmt.Errorf("%s%s %q must not have leading or trailing whitespace", prefix, where, target)
	}
	if strings.IndexFunc(target, isControl) >= 0 {
		return fmt.Errorf("%s%s %q must not contain control characters", prefix, where, target)
	}
	if strings.Contains(target, "#") {
		return fmt.Errorf("%s%s %q must not contain a fragment; a fragment is never sent to a server", prefix, where, target)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return fmt.Errorf("%s%s %q is not a valid request path: %w", prefix, where, target, err)
	}
	switch {
	case parsed.Scheme != "":
		return fmt.Errorf("%s%s %q must not be an absolute URL", prefix, where, target)
	case parsed.Host != "":
		return fmt.Errorf("%s%s %q must not name a host", prefix, where, target)
	case !strings.HasPrefix(target, "/"):
		return fmt.Errorf("%s%s %q must start with %q", prefix, where, target, "/")
	}
	return nil
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

// validateServiceProbeEndpoints checks local service targets do not name a
// platform listener and that two service identities do not claim one endpoint.
func (d Descriptor) validateServiceProbeEndpoints() error {
	type listener struct {
		where   string
		address string
	}
	listeners := []listener{
		{"primary.api_address", d.Primary.APIAddress},
		{"primary.nats.cluster_address", d.Primary.NATS.ClusterAddress},
	}
	if d.HasStandby() {
		listeners = append(listeners,
			listener{"standby.api_address", d.Standby.APIAddress},
			listener{"standby.nats.cluster_address", d.Standby.NATS.ClusterAddress},
		)
	}
	platformPorts := make(map[int]string, len(listeners))
	for _, listener := range listeners {
		_, portText, err := net.SplitHostPort(strings.TrimSpace(listener.address))
		if err != nil {
			return fmt.Errorf("deployment descriptor: %s %q must be host:port: %w", listener.where, listener.address, err)
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("deployment descriptor: %s %q has no usable port", listener.where, listener.address)
		}
		if owner, used := platformPorts[port]; used {
			return fmt.Errorf("deployment descriptor: %s and %s are both on port %d; the machine's listeners run together and cannot share one",
				owner, listener.where, port)
		}
		platformPorts[port] = listener.where
	}

	type endpoint struct {
		port int
		path string
	}
	probed := make(map[endpoint]string, len(d.Services))
	for _, service := range d.Services {
		check := service.HealthCheck
		if owner, used := platformPorts[check.Port]; used {
			return fmt.Errorf("deployment descriptor: services[%s].health_check.port %d is %s; a service cannot be probed on a port the platform binds",
				service.Name, check.Port, owner)
		}
		key := endpoint{port: check.Port, path: check.Path}
		if owner, used := probed[key]; used {
			return fmt.Errorf("deployment descriptor: services %q and %q are both probed at port %d path %q; two services may share a port but not an endpoint",
				owner, service.Name, check.Port, check.Path)
		}
		probed[key] = service.Name
	}
	return nil
}

// FreshFor is how long one report on a service probed by this policy stays
// current: two intervals plus one timeout.
//
// Two intervals is what makes a single lost report survivable, since the next is
// already due, and the timeout covers an attempt that took the longest it was
// allowed to before it was published. The descriptor states the same value on
// every machine of the site so receivers agree; this derives it for the local
// services, which is what the stated one is checked against.
func (h HealthCheck) FreshFor() (time.Duration, error) {
	interval, err := time.ParseDuration(strings.TrimSpace(h.Interval))
	if err != nil {
		return 0, err
	}
	timeout, err := time.ParseDuration(strings.TrimSpace(h.Timeout))
	if err != nil {
		return 0, err
	}
	if interval > (time.Duration(1<<63-1)-timeout)/2 {
		return 0, fmt.Errorf("2 * interval %s + timeout %s overflows a duration", interval, timeout)
	}
	return 2*interval + timeout, nil
}

// validateSiteServices checks the static inventory is complete and uniquely
// keyed, and that its entries for this machine match what this machine hosts.
func (d Descriptor) validateSiteServices(hosted map[unitKey]bool) error {
	type machinePolicy struct {
		profile       string
		observerRoles []string
	}
	seen := make(map[unitKey]bool, len(d.SiteServices))
	machines := make(map[string]machinePolicy, len(d.SiteServices))
	machine := strings.TrimSpace(d.Machine)
	local := make(map[unitKey]SiteService, len(hosted))
	for _, unit := range d.SiteServices {
		unitMachine, unitService := strings.TrimSpace(unit.Machine), strings.TrimSpace(unit.Service)
		where := fmt.Sprintf("site_services[%s/%s]", unit.Machine, unit.Service)
		if unitMachine == "" || unitService == "" {
			return fmt.Errorf("deployment descriptor: a site_services entry has no machine or no service")
		}
		if unit.Machine != unitMachine {
			return fmt.Errorf("deployment descriptor: %s.machine %q must not have leading or trailing whitespace", where, unit.Machine)
		}
		if unit.Service != unitService {
			return fmt.Errorf("deployment descriptor: %s.service %q must not have leading or trailing whitespace", where, unit.Service)
		}
		if !slices.Contains(serviceRoles, unit.ServiceRole) {
			return fmt.Errorf("deployment descriptor: %s.service_role %q is not a known role; the known roles are %s",
				where, unit.ServiceRole, strings.Join(serviceRoles, ", "))
		}
		key := unitKey{unitMachine, unitService, unit.ServiceRole}
		if seen[key] {
			return fmt.Errorf("deployment descriptor: %s with role %q is listed more than once; a site names each unit once", where, unit.ServiceRole)
		}
		seen[key] = true
		profile := strings.TrimSpace(unit.MachineProfile)
		if profile == "" {
			return fmt.Errorf("deployment descriptor: %s.machine_profile is required", where)
		}
		if unit.MachineProfile != profile {
			return fmt.Errorf("deployment descriptor: %s.machine_profile %q must not have leading or trailing whitespace", where, unit.MachineProfile)
		}
		if err := validateObserverRoles(where, unit.ObserverRoles); err != nil {
			return err
		}
		if _, err := validatePositiveDescriptorDuration(where+".fresh_for", unit.FreshFor); err != nil {
			return err
		}
		policy := machinePolicy{profile: profile, observerRoles: unit.ObserverRoles}
		if previous, ok := machines[unitMachine]; ok {
			if previous.profile != policy.profile || !slices.Equal(previous.observerRoles, policy.observerRoles) {
				return fmt.Errorf("deployment descriptor: %s disagrees with another service on machine %q about machine_profile or observer_roles",
					where, unitMachine)
			}
		} else {
			machines[unitMachine] = policy
		}
		if unitMachine == machine {
			if !hosted[key] {
				// Check if this service is hosted on the machine with a different role.
				for _, service := range d.Services {
					if strings.TrimSpace(service.Name) == unitService {
						return fmt.Errorf("deployment descriptor: %s.service_role %q is not services[%s].role %q; one service plays one part",
							where, unit.ServiceRole, unitService, service.Role)
					}
				}
				return fmt.Errorf("deployment descriptor: %s names a service this machine does not host", where)
			}
			local[key] = unit
		}
	}
	return d.validateHostedServicesAreListed(local)
}

// validateObserverRoles checks a unit names the platform instances expected to
// report on it: primary alone, or primary and standby, in that order.
//
// A unit with no expected observer could never be anything but Unknown, and one
// naming standby without primary describes a machine that cannot exist: the
// Primary Instance is the one every machine deploys.
func validateObserverRoles(where string, roles []string) error {
	switch {
	case len(roles) == 0:
		return fmt.Errorf("deployment descriptor: %s.observer_roles is required; a unit nobody reports on is never anything but unknown", where)
	case len(roles) > len(observerRoles):
		return fmt.Errorf("deployment descriptor: %s.observer_roles has %d entries; a machine deploys at most %d instances",
			where, len(roles), len(observerRoles))
	}
	if !slices.Equal(roles, observerRoles[:len(roles)]) {
		return fmt.Errorf("deployment descriptor: %s.observer_roles %v must be %v or %v; every machine deploys a primary and the order is fixed",
			where, roles, observerRoles[:1], observerRoles)
	}
	return nil
}

// validateHostedServicesAreListed checks the two halves agree about this
// machine: every service it hosts is a unit of its site, described the same way
// on both sides.
//
// The local half is the only one whose probe policy this descriptor can see, so
// it is the only place the derived values in the inventory can be checked at
// all. A build that got this machine's entry wrong got every other machine's
// wrong the same way, which is what makes checking one of them worth doing.
func (d Descriptor) validateHostedServicesAreListed(local map[unitKey]SiteService) error {
	wantRoles := observerRoles[:1]
	if d.HasStandby() {
		wantRoles = observerRoles
	}
	for _, service := range d.Services {
		name := strings.TrimSpace(service.Name)
		unit, ok := local[unitKey{strings.TrimSpace(d.Machine), name, service.Role}]
		if !ok {
			return fmt.Errorf("deployment descriptor: services[%s] has no site_services entry for machine %q; every hosted service is a unit of its site",
				name, d.Machine)
		}
		where := fmt.Sprintf("site_services[%s/%s]", strings.TrimSpace(d.Machine), name)
		if unit.ServiceRole != service.Role {
			return fmt.Errorf("deployment descriptor: %s.service_role %q is not services[%s].role %q; one service plays one part",
				where, unit.ServiceRole, name, service.Role)
		}
		if unit.MachineProfile != strings.TrimSpace(d.MachineProfile) {
			return fmt.Errorf("deployment descriptor: %s.machine_profile %q is not this machine's profile %q",
				where, unit.MachineProfile, d.MachineProfile)
		}
		if !slices.Equal(unit.ObserverRoles, wantRoles) {
			return fmt.Errorf("deployment descriptor: %s.observer_roles %v is not %v; both of a machine's instances probe every service on it",
				where, unit.ObserverRoles, wantRoles)
		}
		derived, err := service.HealthCheck.FreshFor()
		if err != nil {
			return fmt.Errorf("deployment descriptor: services[%s].health_check: %w", name, err)
		}
		stated, err := time.ParseDuration(strings.TrimSpace(unit.FreshFor))
		if err != nil {
			return fmt.Errorf("deployment descriptor: %s.fresh_for %q is not a valid duration: %w", where, unit.FreshFor, err)
		}
		if stated != derived {
			return fmt.Errorf("deployment descriptor: %s.fresh_for %s is not the %s its probe policy derives",
				where, unit.FreshFor, derived)
		}
	}
	return nil
}

// validatePositiveDescriptorDuration parses a descriptor duration and requires
// it to be positive, naming the field so a failed startup says what to fix.
func validatePositiveDescriptorDuration(where, value string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return 0, fmt.Errorf("deployment descriptor: %s is required", where)
	}
	d, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("deployment descriptor: %s %q is not a valid duration: %w", where, value, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("deployment descriptor: %s %s must be positive", where, d)
	}
	return d, nil
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
	if _, err := requiredField(instance, string(role)+".api_address"); err != nil {
		return err
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
	return validateNATSFields(role, instance)
}

// validateNATSFields checks one instance's embedded event fabric record is
// present and complete.
//
// It is required for the same reason the listener timeouts are: the instance
// starts its embedded server before it runs, and both fields would decode as the
// empty string if omitted. An unnamed server and an empty listen address are not
// values the runtime could fall back to, so a truncated descriptor fails at load
// rather than when the fabric is first needed.
func validateNATSFields(role PlatformInstanceRole, instance map[string]json.RawMessage) error {
	raw, err := requiredField(instance, string(role)+".nats")
	if err != nil {
		return err
	}
	var nats map[string]json.RawMessage
	if err := json.Unmarshal(raw, &nats); err != nil {
		return fmt.Errorf("deployment descriptor: invalid %s.nats: %w", role, err)
	}
	for _, field := range []string{"server_name", "cluster_name", "cluster_address"} {
		if _, err := requiredField(nats, string(role)+".nats."+field); err != nil {
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
	for _, field := range []string{"duration", "renewal_interval", "health_check_interval", "failback_stabilization"} {
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
	// NATS is this instance's embedded event fabric server. Every deployed
	// instance runs one for its whole lifetime, Active or Passive, so the record
	// is a value rather than an optional block.
	NATS NATS `json:"nats"`
}

// NATS is one instance's embedded event fabric server: what it is called, which
// cluster it belongs to, and where its peers reach it.
//
// The server belongs to the instance level and runs for as long as the process
// does. The client that reaches it belongs to the site level and reaches it in
// process, through the server's own pipe, which is why there is no client
// address here: the server binds no client listener at all.
type NATS struct {
	// ServerName is the embedded server's identity, resolved by the builder as
	// "<machine>-<role>" and unique within the project.
	ServerName string `json:"server_name"`
	// ClusterName is the NATS cluster this server belongs to, resolved from the
	// site's authored cluster name. Servers route only to peers naming the same
	// cluster, so it is what bounds the fabric to the site.
	ClusterName string `json:"cluster_name"`
	// ClusterAddress is where this server accepts route connections from its
	// peers: the machine's own ip joined to the instance's authored cluster
	// port. It is the only address the server binds, and the one listener in the
	// descriptor that is not on loopback, because the site's cluster spans
	// machines.
	ClusterAddress string `json:"cluster_address"`
	// Routes are the peers this server dials to join the cluster, as NATS route
	// URLs. They are every other deployed instance at the site, this machine's
	// other instance included.
	//
	// It is absent exactly when the site deploys one instance in total. Unlike
	// every other field here, absent and empty say the same true thing — this
	// server has no peer — so there is nothing to guard at load.
	Routes []string `json:"routes,omitempty"`
}
