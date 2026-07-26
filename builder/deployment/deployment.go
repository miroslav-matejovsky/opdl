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
// A machine deploys one or two platform instances, and each is an independent
// runtime with its own endpoints. The descriptor is still per machine because one
// binary is built per machine and both instances run from it, so it states the
// machine's identity once and every instance's endpoints separately.
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
	// Instances is this machine's Primary and Standby Instances. Both records are
	// always present.
	Instances Instances `json:"instances"`
	// Lease is the machine's resolved local Primary Ownership lease. Present only
	// when the Standby Instance is deployed; omitted on a standby-less machine.
	Lease *Lease `json:"lease,omitempty"`
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

// Lease is the machine's resolved local Primary Ownership lease: the shared
// machine-wide file its two instances record ownership in, and the timings that
// govern how ownership is held, renewed, and turned over.
//
// It is recorded here rather than derived at runtime because the file path and
// the failover timings are deployment policy an operator must be able to read in
// deployment.json, exactly as the lock's kernel object name was.
//
// It is the machine's, not an instance's: the lease file is the one thing the two
// instances share, and it is what makes exactly one of them Active. It is present
// only when the Standby Instance is deployed (Instances.Standby.Disabled is
// false); on a standby-less machine, there is no lease and no contention.
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
		return fmt.Errorf("lease is required when instances.standby.disabled is false")
	}
	if strings.TrimSpace(l.File) == "" {
		return fmt.Errorf("lease.file is required")
	}
	duration, err := validateLeaseDuration("lease.duration", l.Duration)
	if err != nil {
		return err
	}
	renewal, err := validateLeaseDuration("lease.renewal_interval", l.RenewalInterval)
	if err != nil {
		return err
	}
	if _, err := validateLeaseDuration("lease.health_check_interval", l.HealthCheckInterval); err != nil {
		return err
	}
	if _, err := validateLeaseDuration("lease.failback_stabilization", l.FailbackStabilization); err != nil {
		return err
	}
	if renewal >= duration {
		return fmt.Errorf("lease.renewal_interval %s must be shorter than lease.duration %s", l.RenewalInterval, l.Duration)
	}
	return nil
}

// validateLeaseDuration parses one lease duration and requires it to be positive.
func validateLeaseDuration(field, value string) (time.Duration, error) {
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

// Instances is a machine's two platform instances. Both records are always
// present and non-null, so a reader never infers an instance's deployment from an
// omitted field.
//
// Each instance is an independent runtime and owns its own endpoints, so
// everything an instance binds is resolved onto its own record. The machine holds
// no endpoint of its own.
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

// Instance is one platform instance: whether it is deployed, what the service
// running it is called, and every endpoint it binds.
//
// The endpoint fields are present exactly when the instance is deployed. A
// disabled standby carries nothing but Disabled, so a reader cannot mistake a
// resolved endpoint for one that will ever be bound.
type Instance struct {
	// Disabled reports that this instance is not deployed. It is always false for
	// the primary: a machine with no Primary Instance would deploy nothing that
	// can serve.
	Disabled bool `json:"disabled"`
	// Service is the instance's Windows Service identity. It is carried for
	// whoever installs the services; the runtime does not read it and the platform
	// manages no services.
	Service *WinService `json:"service,omitempty"`
	// DataDir is the instance's own general platform data root.
	DataDir string `json:"data_dir,omitempty"`
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
	APIAddress string `json:"api_address,omitempty"`
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
	if d.Instances.Standby.Disabled {
		if d.Lease != nil {
			return fmt.Errorf("lease is set but instances.standby.disabled is true; omit lease when no standby is deployed")
		}
	} else {
		if err := d.Lease.validate(); err != nil {
			return err
		}
	}
	if d.Instances.Primary.Disabled {
		return fmt.Errorf("instances.primary.disabled: a machine must deploy a Primary Instance")
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
	if d.Instances.Primary.Service == nil {
		return fmt.Errorf("instances.primary.service is required")
	}
	if strings.TrimSpace(d.Instances.Primary.Service.Name) == "" {
		return fmt.Errorf("instances.primary.service.name is required")
	}
	if d.Instances.Standby.Disabled {
		if d.Instances.Standby.Service != nil {
			return fmt.Errorf("instances.standby.service is set but the standby is disabled")
		}
		return nil
	}
	if d.Instances.Standby.Service == nil {
		return fmt.Errorf("instances.standby.service is required when the standby is deployed")
	}
	if strings.TrimSpace(d.Instances.Standby.Service.Name) == "" {
		return fmt.Errorf("instances.standby.service.name is required")
	}
	if d.Instances.Primary.Service.Name == d.Instances.Standby.Service.Name {
		return fmt.Errorf("the primary and standby instances share service name %q", d.Instances.Primary.Service.Name)
	}
	return nil
}

// validateEndpoints checks each deployed instance carries the endpoints it binds,
// that a disabled standby carries none, and that no two listeners on the machine
// were resolved onto the same address.
//
// The addresses are checked against each other rather than only for validity
// because both instances run at once on one host. Two listeners resolved to one
// address is a machine where the second process cannot start, and every address
// here derives from the same machine ip, so a repeated port is a repeated
// address.
func (d Descriptor) validateEndpoints() error {
	if err := validateInstanceEndpoints("instances.primary", d.Instances.Primary); err != nil {
		return err
	}
	if d.Instances.Standby.Disabled {
		if d.Instances.Standby.APIAddress != "" {
			return fmt.Errorf("instances.standby.api_address is set but the standby is disabled")
		}
		if d.Instances.Standby.DataDir != "" {
			return fmt.Errorf("instances.standby.data_dir is set but the standby is disabled")
		}
		return nil
	}
	if err := validateInstanceEndpoints("instances.standby", d.Instances.Standby); err != nil {
		return err
	}
	if d.Instances.Primary.DataDir == d.Instances.Standby.DataDir {
		return fmt.Errorf("instances.primary.data_dir and instances.standby.data_dir are both %q; the two instances run together and cannot share a platform data directory",
			d.Instances.Primary.DataDir)
	}
	if d.Instances.Primary.APIAddress == d.Instances.Standby.APIAddress {
		return fmt.Errorf("instances.primary.api_address and instances.standby.api_address are both %q; the two instances run together and cannot share a listener",
			d.Instances.Primary.APIAddress)
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
	return nil
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
