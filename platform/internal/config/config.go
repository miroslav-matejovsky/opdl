package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/platform/embedded"
)

// Config is the platform's resolved runtime configuration: a composition of the
// deployment descriptor (see package embedded, which the builder stages before
// compiling) and the platform's TOML configuration file, which supplies settings
// a user must explicitly specify without rebuilding the binary.
type Config struct {
	descriptor        deployment.Descriptor
	instances         map[string]Instance
	eventsDir         string
	readHeaderTimeout time.Duration
	shutdownTimeout   time.Duration
	registration      Registration
}

// Instance is one platform process's effective runtime endpoints after the
// descriptor and matching emergency override are composed.
type Instance struct {
	name    string
	address string
	fabric  Fabric
}

// Address returns the HTTP API listen address.
func (i Instance) Address() string { return i.address }

// Fabric returns this process's effective fabric settings.
func (i Instance) Fabric() Fabric { return i.fabric }

// Load composes a Config from the platform's embedded deployment descriptor and
// the TOML configuration file at configPath. Descriptor endpoints are used
// unless a named instance override is present. The configuration file must
// exist and carry every required common runtime setting.
func Load(configPath string) (*Config, error) {
	d, err := embedded.Deployment()
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	f, err := loadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	readHeaderTimeout, err := validateDuration("read_header_timeout", f.ReadHeaderTimeout)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	shutdownTimeout, err := validateDuration("shutdown_timeout", f.ShutdownTimeout)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if _, err := validateDuration("[registration] reconcile_interval", f.Registration.ReconcileInterval); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if _, err := validateDuration("[fabric.olric] start_timeout", f.Fabric.Olric.StartTimeout); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if _, err := validateDuration("[fabric.olric] shutdown_grace", f.Fabric.Olric.ShutdownGrace); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	instances, err := composeInstances(d, f)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &Config{
		descriptor:        d,
		instances:         instances,
		eventsDir:         f.EventsDir,
		readHeaderTimeout: readHeaderTimeout,
		shutdownTimeout:   shutdownTimeout,
		registration:      f.Registration,
	}, nil
}

// composeInstances makes the descriptor authoritative and applies only the
// socket fields explicitly overridden for a named instance.
func composeInstances(d deployment.Descriptor, f file) (map[string]Instance, error) {
	if len(d.PlatformInstances) < 1 || len(d.PlatformInstances) > 2 {
		return nil, fmt.Errorf("deployment descriptor platform_instances must contain primary and optional secondary, found %d", len(d.PlatformInstances))
	}
	described := make(map[string]bool, len(d.PlatformInstances))
	instances := make(map[string]Instance, len(d.PlatformInstances))
	used := make(map[string]string)
	for index, deployed := range d.PlatformInstances {
		want := deployment.PlatformInstancePrimary
		if index == 1 {
			want = deployment.PlatformInstanceSecondary
		}
		if deployed.Name != want {
			return nil, fmt.Errorf("deployment descriptor platform_instances[%d] is %q, expected %q", index, deployed.Name, want)
		}
		described[deployed.Name] = true
		override := f.Instances[deployed.Name]
		effective := Instance{
			name:    deployed.Name,
			address: deployed.APIAddress,
			fabric: Fabric{Olric: FabricOlric{
				ClientAddress:     deployed.FabricClientAddress,
				MemberlistAddress: deployed.FabricMemberlistAddress,
				StartTimeout:      f.Fabric.Olric.StartTimeout,
				ShutdownGrace:     f.Fabric.Olric.ShutdownGrace,
			}},
		}
		if override.APIAddress != nil {
			effective.address = *override.APIAddress
		}
		if override.Fabric.Olric.ClientAddress != nil {
			effective.fabric.Olric.ClientAddress = *override.Fabric.Olric.ClientAddress
		}
		if override.Fabric.Olric.MemberlistAddress != nil {
			effective.fabric.Olric.MemberlistAddress = *override.Fabric.Olric.MemberlistAddress
		}
		if override.Fabric.Olric.Join != nil {
			effective.fabric.Olric.Join = make([]string, len(override.Fabric.Olric.Join))
			copy(effective.fabric.Olric.Join, override.Fabric.Olric.Join)
		}
		for _, endpoint := range []struct {
			field   string
			address string
		}{
			{field: "api_address", address: effective.address},
			{field: "fabric_client_address", address: effective.fabric.Olric.ClientAddress},
			{field: "fabric_memberlist_address", address: effective.fabric.Olric.MemberlistAddress},
		} {
			field, address := endpoint.field, endpoint.address
			if err := validateAddress(address); err != nil {
				return nil, fmt.Errorf("platform instance %q %s: %w", deployed.Name, field, err)
			}
			if owner, exists := used[address]; exists {
				return nil, fmt.Errorf("platform endpoint %q for %s %s is already used by %s", address, deployed.Name, field, owner)
			}
			used[address] = deployed.Name + " " + field
		}
		instances[deployed.Name] = effective
	}
	for name := range f.Instances {
		if !described[name] {
			return nil, fmt.Errorf("platform instance override %q is not present in deployment descriptor", name)
		}
	}
	return instances, nil
}

func validateDuration(name, s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", name, s, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("invalid %s %s: duration must be positive", name, d)
	}
	return d, nil
}

// validateAddress checks addr is a host:port the platform can listen on. The host
// may be empty (all interfaces); the port must be a number in range.
func validateAddress(addr string) error {
	if strings.TrimSpace(addr) != addr || addr == "" {
		return fmt.Errorf("invalid address %q: must be a non-blank host:port without surrounding whitespace", addr)
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid address %q: %w", addr, err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("invalid address %q: port is not a number", addr)
	}
	if n < 1 || n > 65535 {
		return fmt.Errorf("invalid address %q: port %d out of range 1-65535", addr, n)
	}
	return nil
}

// Descriptor returns the deployment descriptor the platform booted with.
func (c *Config) Descriptor() deployment.Descriptor { return c.descriptor }

// Instance returns the effective configuration for a descriptor instance.
func (c *Config) Instance(name string) (Instance, error) {
	instance, found := c.instances[name]
	if !found {
		return Instance{}, fmt.Errorf("platform instance %q is not present in deployment descriptor", name)
	}
	return instance, nil
}

// EventsDir returns the directory the platform records events into. An empty
// string means event recording is disabled.
func (c *Config) EventsDir() string { return c.eventsDir }

// ReadHeaderTimeout returns the maximum duration allowed for reading HTTP request headers.
func (c *Config) ReadHeaderTimeout() time.Duration { return c.readHeaderTimeout }

// ShutdownTimeout returns the maximum duration allowed for graceful server and fabric shutdown.
func (c *Config) ShutdownTimeout() time.Duration { return c.shutdownTimeout }

// Registration returns the registration settings from the configuration file.
func (c *Config) Registration() Registration { return c.registration }

// Summary renders the effective configuration as a human-readable block for
// logging at startup.
func (c *Config) Summary() string {
	d := c.descriptor
	var b strings.Builder
	fmt.Fprintf(&b, "platform configuration (machine=%s):\n", d.Machine)
	fmt.Fprintf(&b, "  deployment descriptor (embedded, staged by builder):\n")
	fmt.Fprintf(&b, "    platform     %s\n", d.Platform)
	fmt.Fprintf(&b, "    project      %s\n", d.Project)
	fmt.Fprintf(&b, "    environment  %s\n", d.Environment)
	fmt.Fprintf(&b, "    site         %s\n", d.Site)
	fmt.Fprintf(&b, "    machine      %s\n", d.Machine)
	fmt.Fprintf(&b, "    role         %s\n", d.Role)
	fmt.Fprintf(&b, "    ip           %s\n", d.IP)
	fmt.Fprintf(&b, "    services     %s\n", strings.Join(d.Services, ", "))
	fmt.Fprintf(&b, "    features     chaos=%t\n", d.Features.Chaos)
	for _, deployed := range d.PlatformInstances {
		instance := c.instances[deployed.Name]
		fmt.Fprintf(&b, "    instance     %s api=%s fabric_client=%s fabric_memberlist=%s\n",
			instance.name, instance.address, instance.fabric.Olric.ClientAddress, instance.fabric.Olric.MemberlistAddress)
	}
	fmt.Fprintf(&b, "    fabric       %s\n", fabricSummary(d.Fabric))
	fmt.Fprintf(&b, "  configuration file (TOML, user-provided):\n")
	fmt.Fprintf(&b, "    events_dir          %s\n", eventsDirSummary(c.eventsDir))
	fmt.Fprintf(&b, "    read_header_timeout %s\n", c.readHeaderTimeout)
	fmt.Fprintf(&b, "    shutdown_timeout    %s\n", c.shutdownTimeout)
	fmt.Fprintf(&b, "    fabric.olric        start_timeout=%s shutdown_grace=%s\n",
		c.instances[deployment.PlatformInstancePrimary].fabric.Olric.StartTimeout,
		c.instances[deployment.PlatformInstancePrimary].fabric.Olric.ShutdownGrace)
	fmt.Fprintf(&b, "    registration        %s", registrationSummary(c.registration))
	return b.String()
}

// registrationSummary renders the registration settings.
func registrationSummary(r Registration) string {
	return "reconcile_interval=" + r.ReconcileInterval
}

// fabricSummary renders the derived fabric membership: who this machine is on
// the fabric and which peers it expects to meet.
func fabricSummary(f deployment.Fabric) string {
	if len(f.Peers) == 0 {
		return "one-member site"
	}
	peers := make([]string, 0, len(f.Peers))
	for _, peer := range f.Peers {
		peers = append(peers, fmt.Sprintf("%s/%s (%s)", peer.Machine, peer.Instance, peer.IP))
	}
	return fmt.Sprintf("peers: %s", strings.Join(peers, ", "))
}

// eventsDirSummary renders an unset events directory as an explicit statement
// that recording is off, so the startup block never shows a blank value.
func eventsDirSummary(dir string) string {
	if dir == "" {
		return "(disabled)"
	}
	return dir
}
