package deployment

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

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
	// DataDir is the instance's own general platform data root.
	DataDir string `json:"data_dir"`
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
	return d.validateEndpoints()
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
// and that no two listeners on the machine were resolved onto the same address.
//
// The addresses are checked against each other rather than only for validity
// because both instances run at once on one host. Two listeners resolved to one
// address is a machine where the second process cannot start, and every address
// here derives from the same machine ip, so a repeated port is a repeated
// address.
func (d Descriptor) validateEndpoints() error {
	if err := validateInstanceEndpoints("primary", d.Primary); err != nil {
		return err
	}
	if !d.HasStandby() {
		return nil
	}
	if err := validateInstanceEndpoints("standby", *d.Standby); err != nil {
		return err
	}
	if d.Primary.DataDir == d.Standby.DataDir {
		return fmt.Errorf("primary.data_dir and standby.data_dir are both %q; the two instances run together and cannot share a platform data directory",
			d.Primary.DataDir)
	}
	if d.Primary.APIAddress == d.Standby.APIAddress {
		return fmt.Errorf("primary.api_address and standby.api_address are both %q; the two instances run together and cannot share a listener",
			d.Primary.APIAddress)
	}
	return nil
}

func validateInstanceEndpoints(prefix string, instance Instance) error {
	if err := requireAddress(prefix+".api_address", instance.APIAddress); err != nil {
		return err
	}
	if err := requireLoopback(prefix+".api_address", instance.APIAddress); err != nil {
		return err
	}
	if strings.TrimSpace(instance.DataDir) == "" {
		return fmt.Errorf("%s.data_dir is required", prefix)
	}
	if _, err := validatePositiveDuration(prefix+".api_read_header_timeout", instance.APIReadHeaderTimeout); err != nil {
		return err
	}
	_, err := validatePositiveDuration(prefix+".api_shutdown_timeout", instance.APIShutdownTimeout)
	return err
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
