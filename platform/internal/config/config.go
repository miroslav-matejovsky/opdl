package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/platform/embedded"
)

// defaultAddress is the listen address the platform serves on when the TOML
// configuration file is absent or leaves it unset. It binds the loopback
// interface so a clean checkout runs without tripping host firewall prompts.
const defaultAddress = "127.0.0.1:8080"

// Config is the platform's resolved runtime configuration: a composition of the
// deployment descriptor (see package embedded, which the builder stages before
// compiling) and the platform's TOML configuration file, which supplies settings
// a user may override without rebuilding the binary. For now that is the address
// the platform's API listens on and where it records events.
type Config struct {
	descriptor   deployment.Descriptor
	address      string
	eventsDir    string
	fabric       Fabric
	registration Registration
}

// Load composes a Config from the platform's embedded deployment descriptor and
// the TOML configuration file at configPath. A missing file is not an error: the
// platform falls back to built-in defaults so it runs standalone. It fails fast
// on malformed embedded data or a malformed configuration file.
func Load(configPath string) (*Config, error) {
	d, err := embedded.Deployment()
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	f, err := loadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &Config{
		descriptor:   d,
		address:      f.Address,
		eventsDir:    f.EventsDir,
		fabric:       f.Fabric,
		registration: f.Registration,
	}, nil
}

// validateAddress checks addr is a host:port the platform can listen on. The host
// may be empty (all interfaces); the port must be a number in range.
func validateAddress(addr string) error {
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

// Address returns the host:port the platform's API listens on.
func (c *Config) Address() string { return c.address }

// EventsDir returns the directory the platform records events into. An empty
// string means event recording is disabled.
func (c *Config) EventsDir() string { return c.eventsDir }

// Fabric returns the fabric adapter overrides from the configuration file. Only
// runtime composition reads it: it is how a site moves the fabric's sockets, and
// no domain package has any business knowing a backend is configurable.
func (c *Config) Fabric() Fabric { return c.fabric }

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
	fmt.Fprintf(&b, "    features     chaos=%t redundancy=%t\n", d.Features.Chaos, d.Features.Redundancy)
	fmt.Fprintf(&b, "    fabric       %s\n", fabricSummary(d.Fabric))
	fmt.Fprintf(&b, "  configuration file (TOML, user-provided):\n")
	fmt.Fprintf(&b, "    address      %s\n", c.address)
	fmt.Fprintf(&b, "    events_dir   %s\n", eventsDirSummary(c.eventsDir))
	fmt.Fprintf(&b, "    fabric.olric %s\n", olricSummary(c.fabric.Olric))
	fmt.Fprintf(&b, "    registration %s", registrationSummary(c.registration))
	return b.String()
}

// registrationSummary renders the registration settings, so a startup log shows
// whether this machine reconciles on its own schedule or the built-in one.
func registrationSummary(r Registration) string {
	if r.ReconcileInterval == "" {
		return "(defaults)"
	}
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
		peers = append(peers, fmt.Sprintf("%s (%s)", peer.Machine, peer.IP))
	}
	return fmt.Sprintf("peers: %s", strings.Join(peers, ", "))
}

// olricSummary renders the fabric adapter overrides, so a startup log shows
// whether a machine is running on its deployment addresses or on local ones.
func olricSummary(o FabricOlric) string {
	overrides := make([]string, 0, 4)
	if o.ClientAddress != "" {
		overrides = append(overrides, "client="+o.ClientAddress)
	}
	if o.MemberlistAddress != "" {
		overrides = append(overrides, "memberlist="+o.MemberlistAddress)
	}
	if o.Join != nil {
		overrides = append(overrides, "join="+strings.Join(o.Join, ","))
	}
	if o.StartTimeout != "" {
		overrides = append(overrides, "start_timeout="+o.StartTimeout)
	}
	if len(overrides) == 0 {
		return "(derived from deployment)"
	}
	return strings.Join(overrides, " ")
}

// eventsDirSummary renders an unset events directory as an explicit statement
// that recording is off, so the startup block never shows a blank value.
func eventsDirSummary(dir string) string {
	if dir == "" {
		return "(disabled)"
	}
	return dir
}
