package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/platform/embedded"
)

// defaultAddress is the listen address the platform serves on when the JSON
// configuration file is absent or leaves it unset. It binds the loopback
// interface so a clean checkout runs without tripping host firewall prompts.
const defaultAddress = "127.0.0.1:8080"

// Config is the platform's resolved runtime configuration: a composition of the
// deployment descriptor (see package embedded, which the builder stages before
// compiling) and the platform's JSON configuration file, which supplies settings
// a user may override without rebuilding the binary. For now that is the address
// the platform's API listens on and where it records events.
type Config struct {
	descriptor   deployment.Descriptor
	address      string
	eventsDir    string
	fabric       Fabric
	registration Registration
}

// file is the schema of the platform's JSON configuration file. It carries the
// settings a user may set without touching the embedded deployment descriptor.
type file struct {
	Address string `json:"address"`
	// EventsDir is optional: empty disables event recording.
	EventsDir string `json:"events_dir"`
	// Fabric is optional: every field falls back to the descriptor's topology.
	Fabric Fabric `json:"fabric"`
	// Registration is optional: every field falls back to a built-in default.
	Registration Registration `json:"registration"`
}

// Registration carries the runtime settings of the registration use case.
type Registration struct {
	// ReconcileInterval overrides how often this platform instance scans the
	// site's registration requests, as a Go duration such as "500ms". Empty
	// keeps the built-in default.
	//
	// It is a latency setting, not a correctness one. Every registration
	// decision is idempotent and derived from the site's state, so a shorter
	// interval accepts requests sooner and a longer one costs less; neither
	// changes what the site decides.
	ReconcileInterval string `json:"reconcile_interval"`
}

// Fabric carries per-adapter runtime overrides for the platform fabric. It is
// keyed by adapter because the settings are adapter-specific by nature; the
// fabric abstraction itself has nothing to configure.
type Fabric struct {
	// Olric configures the embedded Olric adapter.
	Olric FabricOlric `json:"olric"`
}

// FabricOlric are the Olric adapter's runtime overrides.
//
// They exist for development hosts where the deployment's real addresses are not
// bindable, and for scenarios that run several machines on one host. Production
// needs none of them: the adapter derives everything from the descriptor.
//
// These settings move sockets and nothing else. None of them changes which
// machine this is: identity, and therefore a registration's machine and IP, come
// from the embedded descriptor alone and are never configurable at a site.
type FabricOlric struct {
	// ClientAddress overrides the member's client host:port.
	ClientAddress string `json:"client_address"`
	// MemberlistAddress overrides the member's membership host:port.
	MemberlistAddress string `json:"memberlist_address"`
	// Join overrides the memberlist addresses of the peers to seed from. An
	// explicit empty list is not an override; omit the field to keep the peers
	// the descriptor derived.
	Join []string `json:"join"`
	// StartTimeout overrides the readiness bound, as a Go duration such as
	// "45s". Empty keeps the adapter's default.
	StartTimeout string `json:"start_timeout"`
}

// Load composes a Config from the platform's embedded deployment descriptor and
// the JSON configuration file at configPath. A missing file is not an error: the
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

// loadFile reads and validates the JSON configuration file. A file that does not
// exist yields the defaults; a file that exists must be well-formed and, if it
// sets an address, carry a valid host:port.
func loadFile(path string) (file, error) {
	f := file{Address: defaultAddress}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return file{}, fmt.Errorf("read configuration file %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return file{}, fmt.Errorf("invalid configuration file %s: %w", path, err)
	}
	if strings.TrimSpace(f.Address) == "" {
		f.Address = defaultAddress
	}
	if err := validateAddress(f.Address); err != nil {
		return file{}, fmt.Errorf("configuration file %s: %w", path, err)
	}
	// An events directory is not validated here. A path is only known to be
	// usable once it is opened, so the runtime validates it by constructing the
	// sink at startup rather than trusting a check that could go stale.
	f.EventsDir = strings.TrimSpace(f.EventsDir)
	// Fabric overrides are not validated here either: what makes an address
	// usable is the adapter's business, so the composed adapter configuration is
	// validated at startup, before any listener opens.
	f.Fabric.Olric.ClientAddress = strings.TrimSpace(f.Fabric.Olric.ClientAddress)
	f.Fabric.Olric.MemberlistAddress = strings.TrimSpace(f.Fabric.Olric.MemberlistAddress)
	f.Fabric.Olric.StartTimeout = strings.TrimSpace(f.Fabric.Olric.StartTimeout)
	f.Registration.ReconcileInterval = strings.TrimSpace(f.Registration.ReconcileInterval)
	return f, nil
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
	fmt.Fprintf(&b, "  configuration file (JSON, user-provided):\n")
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
