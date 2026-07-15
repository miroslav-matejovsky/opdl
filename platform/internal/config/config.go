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
	descriptor deployment.Descriptor
	address    string
	eventsDir  string
}

// file is the schema of the platform's JSON configuration file. It carries the
// settings a user may set without touching the embedded deployment descriptor.
type file struct {
	Address string `json:"address"`
	// EventsDir is optional: empty disables event recording.
	EventsDir string `json:"events_dir"`
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
	return &Config{descriptor: d, address: f.Address, eventsDir: f.EventsDir}, nil
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
	fmt.Fprintf(&b, "  configuration file (JSON, user-provided):\n")
	fmt.Fprintf(&b, "    address      %s\n", c.address)
	fmt.Fprintf(&b, "    events_dir   %s", eventsDirSummary(c.eventsDir))
	return b.String()
}

// eventsDirSummary renders an unset events directory as an explicit statement
// that recording is off, so the startup block never shows a blank value.
func eventsDirSummary(dir string) string {
	if dir == "" {
		return "(disabled)"
	}
	return dir
}
