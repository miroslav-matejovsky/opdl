package deployment

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// routeScheme is the URL scheme a NATS route is written with. It is what says a
// peer address is dialed as a route rather than as a client connection.
const routeScheme = "nats"

// Descriptor is one machine's deployment definition: everything the platform
// needs to run on that machine, projected from the project blueprint. It is the
// builder's output contract; the platform runtime conforms to it.
//
// One binary is built per machine and both of its instances run from it, so the
// descriptor states the machine's identity once and each instance's own
// endpoints on its own record.
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
	// Services are the services this machine hosts, each with the probe policy
	// the platform runs against it. Both of the machine's instances probe every
	// one of them, in every ownership state.
	//
	// This is the local half of the health contract, and the only half that
	// carries an endpoint. A probe reaches a service on this machine's own ip, so
	// no other machine's descriptor has any use for these ports and paths.
	Services []Service `json:"services"`
	// SiteServices is every deployed service unit at this machine's site,
	// including this machine's own, in one order every machine of the site
	// resolves identically.
	//
	// It is the remote half, and it deliberately carries no endpoint. An instance
	// needs to know which units exist, who is expected to report on each, and how
	// long a report stays fresh, so that a unit nobody has reported on is Unknown
	// rather than absent. It never needs to reach one: only the machine hosting a
	// service probes it.
	SiteServices []SiteService `json:"site_services"`
	// MachineEventsFile is the machine's own append-only event store: the shared
	// file both of its instances append machine-scoped events to.
	//
	// It is the machine's, like the lease, and unlike the lease it is present on
	// every machine. A machine that deploys one instance still has machine facts
	// — which instance owns it, how each activation ended — and they belong in
	// the machine's file rather than in whichever instance happened to state
	// them.
	MachineEventsFile string `json:"machine_events_file"`
	// Primary is the machine's Primary Instance. It is mandatory: a machine with
	// no Primary Instance would deploy nothing that can serve.
	Primary Instance `json:"primary"`
	// Standby is the machine's Standby Instance, present only when the blueprint
	// deploys one. Absence is the whole statement: there is no disabled record to
	// read endpoints off, so an endpoint in a descriptor is always one a process
	// will bind.
	Standby *Instance `json:"standby,omitempty"`
	// Lease is the machine's resolved local Primary Ownership lease. Present
	// exactly when Standby is.
	Lease *Lease `json:"lease,omitempty"`
}

// HasStandby reports whether this machine deploys a Standby Instance.
func (d Descriptor) HasStandby() bool { return d.Standby != nil }

// ServiceNames lists the services this machine hosts, in descriptor order, for
// consumers that place a service rather than probe it.
//
// The package manifest is what wants this: whoever installs a package needs to
// know which services the machine is meant to run, and has no use for the ports
// and paths the platform probes them on.
func (d Descriptor) ServiceNames() []string {
	names := make([]string, 0, len(d.Services))
	for _, service := range d.Services {
		names = append(names, service.Name)
	}
	return names
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

// Service is one service this machine hosts and how the platform learns whether
// it is up.
//
// The name is unique within the machine; a site tells two copies of one service
// apart by the machine each runs on. Which copy leads is Role, and it is carried
// as metadata rather than as a probe input: the platform probes a master and a
// slave the same way and reports what it found.
type Service struct {
	// Name is the service identifier, unique within this machine.
	Name string `json:"name"`
	// Role is the part this machine's copy plays: "master" or "slave".
	Role string `json:"role"`
	// HealthCheck is the probe policy for this service. It is required: a service
	// the platform cannot ask about is one nobody can be told has stopped.
	HealthCheck HealthCheck `json:"health_check"`
}

// HealthCheck is the resolved probe policy for one service: what to ask, how
// often, how long to wait, and how much failure to tolerate before the service
// counts as down.
//
// The durations are Go duration strings ("10s", "2s"), validated by the builder
// and parsed by the platform at startup, exactly like the lease timings.
type HealthCheck struct {
	// Type is the kind of probe. "http" is the only kind today.
	Type string `json:"type"`
	// Port is the port on this machine the probe connects to. It is the service's
	// own listener, never one the platform binds, and two services may share it
	// when they answer on different paths.
	Port int `json:"port"`
	// Path is the request target, as authored: a path and an optional query. It
	// carries no scheme, host, or fragment, because where the probe connects is
	// the machine's ip and this port.
	Path string `json:"path"`
	// Interval is how often the probe runs.
	Interval string `json:"interval"`
	// Timeout bounds one probe attempt. It is shorter than Interval, so a slow
	// probe cannot still be running when the next one is due.
	Timeout string `json:"timeout"`
	// Retries is how many consecutive failed attempts mark the service down. It
	// is at least 1.
	Retries int `json:"retries"`
}

// SiteService is one deployed service unit anywhere at the site, as every
// machine of that site is told about it.
//
// Every descriptor at one site carries the same list in the same order, so two
// instances that received the same reports reduce them to the same view. It is
// the static half of that view: it says what exists and who should be reporting,
// which is what makes a service nobody has reported on Unknown rather than
// missing.
type SiteService struct {
	// Machine is the machine hosting this unit, and MachineProfile is that
	// machine's purpose. Together with Service they key the unit within the site.
	Machine        string `json:"machine"`
	MachineProfile string `json:"machine_profile"`
	// Service is the service name as that machine authored it.
	Service string `json:"service"`
	// ServiceRole is the part that copy plays: "master" or "slave".
	ServiceRole string `json:"service_role"`
	// ObserverRoles are the platform instance roles expected to report on this
	// unit: "primary", plus "standby" when the hosting machine deploys one.
	//
	// Both instances of a machine probe every service on it, so an expected
	// observer that is silent is a fact about the platform rather than about the
	// service, and the two must not look alike.
	ObserverRoles []string `json:"observer_roles"`
	// FreshFor is how long one observer's report on this unit stays current,
	// measured by the receiver from when it arrived.
	//
	// It is derived from this unit's own probe policy rather than authored, so
	// every machine of the site expires the same report at the same age without
	// being told the endpoint that produced it.
	FreshFor string `json:"fresh_for"`
}

// Lease is the machine's resolved local Primary Ownership lease: the shared
// machine-wide file its two instances record ownership in, and the timings that
// govern how ownership is held, renewed, and turned over.
//
// It is recorded here rather than derived at runtime because the file path and
// the failover timings are deployment policy an operator must be able to read in
// deployment.json, exactly as the lock's kernel object name was.
//
// It is the machine's, not an instance's: the lease file is the one thing the two
// instances share, and it is what makes exactly one of them Active. On a
// standby-less machine there is no lease and no contention.
//
// The durations are Go duration strings ("15s", "5s"), validated by the builder
// and parsed by the platform at startup.
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

// validate checks a resolved lease is complete and its timings are usable. It is
// called only when a Standby Instance is deployed; a standby-less machine has no
// lease at all.
func (l *Lease) validate() error {
	if l == nil {
		return fmt.Errorf("lease is required when a standby is deployed")
	}
	if strings.TrimSpace(l.File) == "" {
		return fmt.Errorf("lease.file is required")
	}
	duration, err := validatePositiveDuration("lease.duration", l.Duration)
	if err != nil {
		return err
	}
	renewal, err := validatePositiveDuration("lease.renewal_interval", l.RenewalInterval)
	if err != nil {
		return err
	}
	if _, err := validatePositiveDuration("lease.health_check_interval", l.HealthCheckInterval); err != nil {
		return err
	}
	if _, err := validatePositiveDuration("lease.failback_stabilization", l.FailbackStabilization); err != nil {
		return err
	}
	if renewal >= duration {
		return fmt.Errorf("lease.renewal_interval %s must be shorter than lease.duration %s", l.RenewalInterval, l.Duration)
	}
	return nil
}

// validatePositiveDuration parses one resolved duration string and requires it
// to be positive. Every duration in a descriptor is one: a lease timing, a lag
// bound, or an instance's listener timeout.
func validatePositiveDuration(field, value string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return 0, fmt.Errorf("%s is required", field)
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s %q is not a valid duration: %w", field, value, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s %s must be positive", field, d)
	}
	return d, nil
}

// Instance is one platform instance: what the service running it is called, and
// every endpoint it binds. Both roles use this type; which role a record is for
// is the field it sits in, not a value inside it.
//
// Every field is required. An instance record exists only for an instance that is
// deployed, so nothing here is conditional.
type Instance struct {
	// Service is the instance's Windows Service identity. It is carried for
	// whoever installs the services; the runtime does not read it and the platform
	// manages no services.
	Service *WinService `json:"service,omitempty"`
	// EventsFile is the instance's own append-only JSON Lines event record: the
	// local file it appends every fact it states to, including the ones about
	// failing to start.
	EventsFile string `json:"events_file"`
	// StateFile is the instance's own durable state record, carried across
	// restarts and crashes. It holds the instance's epoch counter, which advances
	// by exactly one every time the process starts and every time the instance
	// becomes Active.
	StateFile string `json:"state_file"`
	// LogFile is the instance's own structured application log: the file its slog
	// records are appended to.
	//
	// It is beside EventsFile rather than part of it because the two are different
	// records. The event log carries the facts the instance stated, which other
	// levels consume and tooling asserts on; the application log carries what the
	// process was doing, which only a human reads.
	LogFile string `json:"log_file"`
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
	APIAddress string `json:"api_address"`
	// APIReadHeaderTimeout bounds how long this instance's listener spends
	// reading an HTTP request's headers before closing the connection. It is a Go
	// duration string ("5s"), parsed by the platform at startup.
	APIReadHeaderTimeout string `json:"api_read_header_timeout"`
	// APIShutdownTimeout bounds the graceful drain of this instance's listener
	// when it stops serving.
	APIShutdownTimeout string `json:"api_shutdown_timeout"`
	// NATS is this instance's embedded event fabric server. Every deployed
	// instance runs one, so the record is a value rather than an optional block:
	// there is no instance in a descriptor that has no fabric.
	NATS NATS `json:"nats"`
}

// NATS is one instance's resolved embedded event fabric server: what it is
// called, which cluster it belongs to, and where its peers reach it.
//
// The server belongs to the instance and runs for the whole life of the
// process, whether that instance is Active or Passive. The client that reaches
// it belongs to the site, and reaches it in process rather than over any
// listener, which is why there is no client address here: the server binds none.
type NATS struct {
	// ServerName is the embedded server's identity, resolved by the builder as
	// "<machine>-<role>". Machine names are unique within a project, so no two
	// servers a project deploys are named the same.
	ServerName string `json:"server_name"`
	// ClusterName is the NATS cluster this server belongs to, resolved by the
	// builder from the site's authored nats.cluster_name.
	//
	// The site is the boundary because the site is what has to converge: servers
	// route only to peers naming the same cluster, so two sites of one project
	// form two clusters and never exchange a message by accident.
	ClusterName string `json:"cluster_name"`
	// ClusterAddress is where this server accepts route connections from its
	// peers: the machine's own ip joined to the instance's authored nats
	// cluster_port.
	//
	// It is the one listener in a descriptor that is not on loopback, and it has
	// to be: the site's cluster spans machines, so a peer on another host has to
	// be able to reach it. It carries no client traffic; the platform's own
	// client never touches a socket.
	ClusterAddress string `json:"cluster_address"`
	// Routes are the peers this server dials to join the cluster, as NATS route
	// URLs ("nats://10.0.1.11:6222").
	//
	// They are every other deployed instance at the site, including the other
	// instance of this machine. The list is empty exactly when the site deploys
	// one instance in total, which is the one case where a member has nobody to
	// route to, so it is omitted rather than written as an empty array.
	Routes []string `json:"routes,omitempty"`
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
	if strings.TrimSpace(d.MachineEventsFile) == "" {
		return fmt.Errorf("machine_events_file is required")
	}
	// The standby policy is settled before the health contract is checked, because
	// which instances are expected to report on this machine's services follows
	// from it. A descriptor that got the standby wrong would otherwise be reported
	// as an inventory that named the wrong observers.
	if !d.HasStandby() {
		if d.Lease != nil {
			return fmt.Errorf("lease is set but no standby is deployed; omit lease when standby is absent")
		}
	} else if err := d.Lease.validate(); err != nil {
		return err
	}
	if err := d.validateHostedServices(); err != nil {
		return err
	}
	if err := d.validateSiteServices(); err != nil {
		return err
	}
	if err := d.validateServices(); err != nil {
		return err
	}
	if err := d.validateEndpoints(); err != nil {
		return err
	}
	return d.validateLocalFiles()
}

// probeTypeHTTP is the one probe kind a descriptor may carry. It is repeated
// here rather than imported from the blueprint because the descriptor is the
// contract between the two tools, and a runtime reading it must be able to
// reject an unknown kind without the build tool present.
const probeTypeHTTP = "http"

// serviceRoles are the parts a machine's copy of a service may play.
var serviceRoles = []string{"master", "slave"}

// observerRoles are the platform instance roles that may be expected to report
// on a unit, in the order a resolved descriptor lists them.
var observerRoles = []string{string(RolePrimary), string(RoleStandby)}

// validateHostedServices checks this machine's own services carry a complete,
// runnable probe policy.
//
// Every field is required rather than defaulted. A probe policy with a zero
// interval is not a policy a runtime could guess at: it is a descriptor that
// lost one, and the difference matters because the platform is about to run
// whatever it reads here against a live service.
func (d Descriptor) validateHostedServices() error {
	if len(d.Services) == 0 {
		return fmt.Errorf("at least one service is required")
	}
	named := make(map[string]bool, len(d.Services))
	for _, service := range d.Services {
		name := strings.TrimSpace(service.Name)
		if name == "" {
			return fmt.Errorf("services: a service has no name")
		}
		if service.Name != name {
			return fmt.Errorf("services[%s].name %q must not have leading or trailing whitespace", name, service.Name)
		}
		if named[name] {
			return fmt.Errorf("services: %q is hosted more than once", name)
		}
		named[name] = true
		if !slices.Contains(serviceRoles, service.Role) {
			return fmt.Errorf("services[%s].role %q is not a known role; the known roles are %s",
				name, service.Role, strings.Join(serviceRoles, ", "))
		}
		if err := service.HealthCheck.validate(fmt.Sprintf("services[%s].health_check", name)); err != nil {
			return err
		}
	}
	return nil
}

// validate checks one resolved probe policy is complete and runnable.
func (h HealthCheck) validate(where string) error {
	if h.Type != probeTypeHTTP {
		return fmt.Errorf("%s.type %q is not a known probe; the known probes are %s", where, h.Type, probeTypeHTTP)
	}
	if h.Port < 1 || h.Port > 65535 {
		return fmt.Errorf("%s.port %d is out of range 1-65535", where, h.Port)
	}
	if err := validateHealthRequestTarget(where+".path", h.Path); err != nil {
		return err
	}
	interval, err := validatePositiveDuration(where+".interval", h.Interval)
	if err != nil {
		return err
	}
	timeout, err := validatePositiveDuration(where+".timeout", h.Timeout)
	if err != nil {
		return err
	}
	if timeout >= interval {
		return fmt.Errorf("%s.timeout %s must be shorter than interval %s", where, h.Timeout, h.Interval)
	}
	if h.Retries < 1 {
		return fmt.Errorf("%s.retries must be at least 1, got %d", where, h.Retries)
	}
	return nil
}

// validateHealthRequestTarget checks an HTTP probe carries only the path and
// optional query sent to the service. The scheme and host come from the probe
// type and machine descriptor.
func validateHealthRequestTarget(where, target string) error {
	if strings.TrimSpace(target) == "" {
		return fmt.Errorf("%s is required", where)
	}
	if target != strings.TrimSpace(target) {
		return fmt.Errorf("%s %q must not have leading or trailing whitespace", where, target)
	}
	if strings.IndexFunc(target, isControl) >= 0 {
		return fmt.Errorf("%s %q must not contain control characters", where, target)
	}
	if strings.Contains(target, "#") {
		return fmt.Errorf("%s %q must not contain a fragment; a fragment is never sent to a server", where, target)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return fmt.Errorf("%s %q is not a valid request path: %w", where, target, err)
	}
	switch {
	case parsed.Scheme != "":
		return fmt.Errorf("%s %q must not be an absolute URL", where, target)
	case parsed.Host != "":
		return fmt.Errorf("%s %q must not name a host", where, target)
	case !strings.HasPrefix(target, "/"):
		return fmt.Errorf("%s %q must start with %q", where, target, "/")
	}
	return nil
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

// freshFor is how long one report on a service stays current: two probe
// intervals plus one timeout.
//
// Two intervals is what makes a single lost report survivable — the next one is
// already on its way — and the timeout covers an attempt that took the longest
// it was allowed to before it was published. It is derived rather than authored
// so every machine of a site expires the same report at the same age.
func (h HealthCheck) freshFor() (time.Duration, error) {
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

// validateSiteServices checks the static site inventory is complete, keyed
// uniquely, and consistent with the services this machine hosts.
//
// The inventory is what every instance reduces reports against, so a machine
// whose own service is missing from it would publish observations nobody could
// place. That is why the local half is cross-checked against the remote half
// here rather than trusted because one resolver produced both.
func (d Descriptor) validateSiteServices() error {
	if len(d.SiteServices) == 0 {
		return fmt.Errorf("site_services is required; it carries this machine's own services too")
	}
	seen := make(map[unitKey]SiteService, len(d.SiteServices))
	machines := make(map[string]siteMachine, len(d.SiteServices))
	for _, unit := range d.SiteServices {
		machine, service := strings.TrimSpace(unit.Machine), strings.TrimSpace(unit.Service)
		where := fmt.Sprintf("site_services[%s/%s]", unit.Machine, unit.Service)
		if machine == "" || service == "" {
			return fmt.Errorf("site_services: an entry has no machine or no service")
		}
		if unit.Machine != machine {
			return fmt.Errorf("%s.machine %q must not have leading or trailing whitespace", where, unit.Machine)
		}
		if unit.Service != service {
			return fmt.Errorf("%s.service %q must not have leading or trailing whitespace", where, unit.Service)
		}
		if _, listed := seen[unitKey{machine, service}]; listed {
			return fmt.Errorf("%s is listed more than once; a site names each unit once", where)
		}
		seen[unitKey{machine, service}] = unit
		profile := strings.TrimSpace(unit.MachineProfile)
		if profile == "" {
			return fmt.Errorf("%s.machine_profile is required", where)
		}
		if unit.MachineProfile != profile {
			return fmt.Errorf("%s.machine_profile %q must not have leading or trailing whitespace", where, unit.MachineProfile)
		}
		if !slices.Contains(serviceRoles, unit.ServiceRole) {
			return fmt.Errorf("%s.service_role %q is not a known role; the known roles are %s",
				where, unit.ServiceRole, strings.Join(serviceRoles, ", "))
		}
		if err := validateObserverRoles(where, unit.ObserverRoles); err != nil {
			return err
		}
		if _, err := validatePositiveDuration(where+".fresh_for", unit.FreshFor); err != nil {
			return err
		}
		policy := siteMachine{profile: profile, observerRoles: unit.ObserverRoles}
		if previous, ok := machines[machine]; ok {
			if previous.profile != policy.profile || !slices.Equal(previous.observerRoles, policy.observerRoles) {
				return fmt.Errorf("%s disagrees with another service on machine %q about machine_profile or observer_roles",
					where, machine)
			}
		} else {
			machines[machine] = policy
		}
	}
	return d.validateHostedServicesAreListed(seen)
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
		return fmt.Errorf("%s.observer_roles is required; a unit nobody reports on is never anything but unknown", where)
	case len(roles) > len(observerRoles):
		return fmt.Errorf("%s.observer_roles has %d entries; a machine deploys at most %d instances", where, len(roles), len(observerRoles))
	}
	if !slices.Equal(roles, observerRoles[:len(roles)]) {
		return fmt.Errorf("%s.observer_roles %v must be %v or %v; every machine deploys a primary and the order is fixed",
			where, roles, observerRoles[:1], observerRoles)
	}
	return nil
}

// unitKey identifies one deployed service unit within a site. A service name is
// unique only within its machine, so the machine is half the key.
type unitKey struct{ machine, service string }

// siteMachine is the machine-level policy repeated on each inventory unit.
// Every service on one machine must repeat the same values.
type siteMachine struct {
	profile       string
	observerRoles []string
}

// validateHostedServicesAreListed checks the two halves of the health contract
// agree about this machine.
//
// The local half is the only one whose probe policy this descriptor can see, so
// it is the only place the derived values in the inventory can be checked at
// all. A machine that got its own entry wrong would have got every other
// machine's wrong the same way, which is what makes checking one of them worth
// doing.
func (d Descriptor) validateHostedServicesAreListed(listed map[unitKey]SiteService) error {
	machine := strings.TrimSpace(d.Machine)
	hosted := make(map[string]bool, len(d.Services))
	for _, service := range d.Services {
		hosted[strings.TrimSpace(service.Name)] = true
	}
	for key := range listed {
		if key.machine == machine && !hosted[key.service] {
			return fmt.Errorf("site_services[%s/%s] names a service this machine does not host", key.machine, key.service)
		}
	}
	for _, service := range d.Services {
		name := strings.TrimSpace(service.Name)
		unit, ok := listed[unitKey{machine, name}]
		if !ok {
			return fmt.Errorf("services[%s] has no site_services entry for machine %q; every hosted service is a unit of its site", name, d.Machine)
		}
		where := fmt.Sprintf("site_services[%s/%s]", machine, name)
		if unit.ServiceRole != service.Role {
			return fmt.Errorf("%s.service_role %q is not services[%s].role %q; one service plays one part",
				where, unit.ServiceRole, name, service.Role)
		}
		if unit.MachineProfile != strings.TrimSpace(d.MachineProfile) {
			return fmt.Errorf("%s.machine_profile %q is not this machine's profile %q", where, unit.MachineProfile, d.MachineProfile)
		}
		// This machine's expected observers are its own deployed instances, which
		// the descriptor already states. An inventory that expected a standby
		// report from a machine deploying none would leave the unit permanently
		// short of an observer and therefore never Healthy.
		wantRoles := observerRoles[:1]
		if d.HasStandby() {
			wantRoles = observerRoles
		}
		if !slices.Equal(unit.ObserverRoles, wantRoles) {
			return fmt.Errorf("%s.observer_roles %v is not %v; both of a machine's instances probe every service on it",
				where, unit.ObserverRoles, wantRoles)
		}
		derived, err := service.HealthCheck.freshFor()
		if err != nil {
			return fmt.Errorf("services[%s].health_check: %w", name, err)
		}
		stated, err := time.ParseDuration(strings.TrimSpace(unit.FreshFor))
		if err != nil {
			return fmt.Errorf("%s.fresh_for %q is not a valid duration: %w", where, unit.FreshFor, err)
		}
		if stated != derived {
			return fmt.Errorf("%s.fresh_for %s is not the %s its probe policy derives",
				where, unit.FreshFor, derived)
		}
	}
	return nil
}

// validateServices checks each instance's Windows Service identity is present
// exactly when that instance is deployed, and that the two differ.
//
// The two instances run on one host, so identical names are the one service
// collision Windows cannot refuse at install time for us.
func (d Descriptor) validateServices() error {
	if err := validateService("primary", d.Primary); err != nil {
		return err
	}
	if !d.HasStandby() {
		return nil
	}
	if err := validateService("standby", *d.Standby); err != nil {
		return err
	}
	if d.Primary.Service.Name == d.Standby.Service.Name {
		return fmt.Errorf("the primary and standby instances share service name %q", d.Primary.Service.Name)
	}
	return nil
}

func validateService(prefix string, instance Instance) error {
	if instance.Service == nil {
		return fmt.Errorf("%s.service is required", prefix)
	}
	if strings.TrimSpace(instance.Service.Name) == "" {
		return fmt.Errorf("%s.service.name is required", prefix)
	}
	return nil
}

// validateEndpoints checks each deployed instance carries the endpoints it binds,
// and that no two listeners on the machine were resolved onto the same port.
//
// The ports are checked against each other rather than only for validity because
// both instances run at once on one host, and each one binds two listeners: its
// API and its embedded event fabric server. Ports rather than addresses, because
// the two kinds no longer sit on one interface: an API is on loopback and a
// cluster address is on the machine's ip. Comparing addresses would call those
// distinct on a machine whose ip is 127.0.0.1, where they are the same socket.
func (d Descriptor) validateEndpoints() error {
	if err := validateInstanceEndpoints("primary", d.IP, d.Primary); err != nil {
		return err
	}
	if d.HasStandby() {
		if err := validateInstanceEndpoints("standby", d.IP, *d.Standby); err != nil {
			return err
		}
	}

	listeners := []struct{ where, address string }{
		{"primary.api_address", d.Primary.APIAddress},
		{"primary.nats.cluster_address", d.Primary.NATS.ClusterAddress},
	}
	if d.HasStandby() {
		listeners = append(listeners,
			struct{ where, address string }{"standby.api_address", d.Standby.APIAddress},
			struct{ where, address string }{"standby.nats.cluster_address", d.Standby.NATS.ClusterAddress},
		)
	}
	taken := make(map[int]string, len(listeners))
	for _, l := range listeners {
		_, portText, err := net.SplitHostPort(l.address)
		if err != nil {
			return fmt.Errorf("%s: %q must be host:port: %w", l.where, l.address, err)
		}
		port, err := strconv.Atoi(portText)
		if err != nil {
			return fmt.Errorf("%s: %q has no usable port", l.where, l.address)
		}
		if owner, used := taken[port]; used {
			return fmt.Errorf("%s and %s are both on port %d; the machine's listeners run together and cannot share one", owner, l.where, port)
		}
		taken[port] = l.where
	}
	return d.validateServiceProbeEndpoints(taken)
}

// validateServiceProbeEndpoints checks local service targets do not name a
// platform listener and that two service identities do not claim one endpoint.
func (d Descriptor) validateServiceProbeEndpoints(platformPorts map[int]string) error {
	type endpoint struct {
		port int
		path string
	}
	probed := make(map[endpoint]string, len(d.Services))
	for _, service := range d.Services {
		check := service.HealthCheck
		if owner, used := platformPorts[check.Port]; used {
			return fmt.Errorf("services[%s].health_check.port %d is %s; a service cannot be probed on a port the platform binds",
				service.Name, check.Port, owner)
		}
		key := endpoint{port: check.Port, path: check.Path}
		if owner, used := probed[key]; used {
			return fmt.Errorf("services %q and %q are both probed at port %d path %q; two services may share a port but not an endpoint",
				owner, service.Name, check.Port, check.Path)
		}
		probed[key] = service.Name
	}
	return nil
}

// validateLocalFiles checks every local file the machine resolved has its own
// path.
//
// Each instance's files belong to that instance, and the two run together on one
// host, so a path resolved onto both is two runtimes writing one file. The
// machine's own store is in the same set: it is not an instance's file either,
// and a machine that wrote its shared account into one instance's record would
// lose it the moment that instance stopped being the one that owns the machine.
func (d Descriptor) validateLocalFiles() error {
	files := []struct{ where, path string }{
		{"machine_events_file", d.MachineEventsFile},
		{"primary.events_file", d.Primary.EventsFile},
		{"primary.state_file", d.Primary.StateFile},
		{"primary.log_file", d.Primary.LogFile},
	}
	if d.HasStandby() {
		files = append(files,
			struct{ where, path string }{"standby.events_file", d.Standby.EventsFile},
			struct{ where, path string }{"standby.state_file", d.Standby.StateFile},
			struct{ where, path string }{"standby.log_file", d.Standby.LogFile},
			struct{ where, path string }{"lease.file", d.Lease.File},
		)
	}

	taken := make(map[string]string, len(files))
	for _, f := range files {
		key := pathKey(f.path)
		if owner, used := taken[key]; used {
			return fmt.Errorf("%s and %s are both %q; every file an instance owns needs its own path", owner, f.where, f.path)
		}
		taken[key] = f.where
	}
	return nil
}

func validateInstanceEndpoints(prefix, machineIP string, instance Instance) error {
	if err := requireAddress(prefix+".api_address", instance.APIAddress); err != nil {
		return err
	}
	if err := requireLoopback(prefix+".api_address", instance.APIAddress); err != nil {
		return err
	}
	if strings.TrimSpace(instance.EventsFile) == "" {
		return fmt.Errorf("%s.events_file is required", prefix)
	}
	if strings.TrimSpace(instance.StateFile) == "" {
		return fmt.Errorf("%s.state_file is required", prefix)
	}
	if strings.TrimSpace(instance.LogFile) == "" {
		return fmt.Errorf("%s.log_file is required", prefix)
	}
	if pathKey(instance.EventsFile) == pathKey(instance.StateFile) {
		return fmt.Errorf("%s.events_file and %s.state_file are both %q; every file an instance owns needs its own path", prefix, prefix, instance.EventsFile)
	}
	if _, err := validatePositiveDuration(prefix+".api_read_header_timeout", instance.APIReadHeaderTimeout); err != nil {
		return err
	}
	if _, err := validatePositiveDuration(prefix+".api_shutdown_timeout", instance.APIShutdownTimeout); err != nil {
		return err
	}
	if strings.TrimSpace(instance.NATS.ServerName) == "" {
		return fmt.Errorf("%s.nats.server_name is required", prefix)
	}
	if strings.TrimSpace(instance.NATS.ClusterName) == "" {
		return fmt.Errorf("%s.nats.cluster_name is required", prefix)
	}
	if err := requireAddress(prefix+".nats.cluster_address", instance.NATS.ClusterAddress); err != nil {
		return err
	}
	if err := requireMachineHost(prefix+".nats.cluster_address", machineIP, instance.NATS.ClusterAddress); err != nil {
		return err
	}
	return validateRoutes(prefix, instance.NATS)
}

// validateRoutes checks the peers this instance's embedded server dials are
// usable route URLs, distinct, and not this server's own listener.
//
// An empty list is valid: a site that deploys one instance in total has no peer
// to route to. What is not valid is a route to this instance's own cluster
// address, which would be a server dialing itself, or one peer listed twice,
// which is a resolution that lost track of the site's membership.
func validateRoutes(prefix string, nats NATS) error {
	seen := make(map[string]int, len(nats.Routes))
	for index, route := range nats.Routes {
		where := fmt.Sprintf("%s.nats.routes[%d]", prefix, index)
		address, err := routeAddress(route)
		if err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		if address == nats.ClusterAddress {
			return fmt.Errorf("%s %q is this instance's own cluster address; a server's routes are its peers", where, route)
		}
		if first, repeated := seen[address]; repeated {
			return fmt.Errorf("%s and %s.nats.routes[%d] are both %q; each peer is routed to once", where, prefix, first, route)
		}
		seen[address] = index
	}
	return nil
}

// routeAddress returns the host:port a route URL points at, checking the URL is
// one the embedded server can dial.
func routeAddress(route string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(route))
	if err != nil {
		return "", fmt.Errorf("route %q is not a URL: %w", route, err)
	}
	if parsed.Scheme != routeScheme {
		return "", fmt.Errorf("route %q must use the %s:// scheme", route, routeScheme)
	}
	if err := validateAddress(parsed.Host); err != nil {
		return "", fmt.Errorf("route %q: %w", route, err)
	}
	return parsed.Host, nil
}

// pathKey normalizes a resolved path for comparison. This repo is Windows-only,
// so two paths that differ only in separators or case name one file, and
// comparing them literally would let a descriptor resolve two instances onto it.
func pathKey(path string) string {
	return strings.ToLower(filepath.Clean(strings.TrimSpace(path)))
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
//
// The platform API is what this holds for. It answers for the instance running
// on its own host, to an operator or a co-located service, and is never reached
// from another machine. The event fabric's cluster address is the deliberate
// exception and is checked by requireMachineHost instead.
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

// requireMachineHost checks a host:port field is bound on the machine's own ip.
//
// The event fabric's cluster address is what this holds for, and it is why the
// machine states an ip at all. The site's embedded servers form one cluster
// across machines, so each one has to be reachable at an address its peers can
// dial. Binding it anywhere else is either unreachable to the site or a listener
// on an interface the deployment did not declare.
func requireMachineHost(where, machineIP, addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%s: %q must be host:port: %w", where, addr, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.Equal(net.ParseIP(machineIP)) {
		return fmt.Errorf("%s %q is not on the machine's ip %q; the site's event fabric peers reach this server there", where, addr, machineIP)
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
