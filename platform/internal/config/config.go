package config

import (
	"fmt"
	"strings"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/platform/embedded"
)

// Config is the platform's resolved runtime configuration: a composition of
// the deployment descriptor (see package embedded, which the builder stages
// before compiling) and any other embedded configuration the platform grows.
type Config struct {
	descriptor deployment.Descriptor
}

// Load composes a Config from the platform's embedded configuration. It fails
// fast on malformed embedded data.
func Load() (*Config, error) {
	d, err := embedded.Deployment()
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &Config{descriptor: d}, nil
}

// Descriptor returns the deployment descriptor the platform booted with.
func (c *Config) Descriptor() deployment.Descriptor { return c.descriptor }

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
	fmt.Fprintf(&b, "    features     chaos=%t redundancy=%t", d.Features.Chaos, d.Features.Redundancy)
	return b.String()
}
