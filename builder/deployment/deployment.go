package deployment

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
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
	// Services are the service groups this machine hosts.
	Services []string `json:"services"`
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
	// LagBound is how far a process's projection may fall behind the journal
	// before it stops being promotable, and before an Active process stops
	// serving rather than answering from a stale view. It is carried on the lease
	// because it bounds a failover, which is a question only a machine that
	// deploys a standby asks.
	LagBound string `json:"lag_bound"`
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
	if _, err := validatePositiveDuration("lease.lag_bound", l.LagBound); err != nil {
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
	if len(d.Services) == 0 {
		return fmt.Errorf("at least one service is required")
	}
	if strings.TrimSpace(d.MachineEventsFile) == "" {
		return fmt.Errorf("machine_events_file is required")
	}
	if !d.HasStandby() {
		if d.Lease != nil {
			return fmt.Errorf("lease is set but no standby is deployed; omit lease when standby is absent")
		}
	} else if err := d.Lease.validate(); err != nil {
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
	taken := make(map[string]string, len(listeners))
	for _, l := range listeners {
		_, port, err := net.SplitHostPort(l.address)
		if err != nil {
			return fmt.Errorf("%s: %q must be host:port: %w", l.where, l.address, err)
		}
		if owner, used := taken[port]; used {
			return fmt.Errorf("%s and %s are both on port %s; the machine's listeners run together and cannot share one", owner, l.where, port)
		}
		taken[port] = l.where
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
