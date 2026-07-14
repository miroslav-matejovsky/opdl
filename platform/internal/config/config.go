package config

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
)

// defaultPort is the platform's default listen port, applied when the embedded
// configuration leaves it unset.
const defaultPort = 8080

// platformJSON is the platform's embedded configuration: a neutral mock the
// platform runs with until the builder stages a real per-machine descriptor.
// Keeping it as embedded data means the platform always has a valid
// configuration to boot with.
//
//go:embed platform.json
var platformJSON []byte

// Runtime holds the platform's runtime parameters: operational settings the
// platform runs with, owned by the platform rather than the deployment
// descriptor. It is kept deliberately small here (just the listen port) to
// demonstrate the runtime-parameter tier; more parameters join it as the
// platform grows.
type Runtime struct {
	// Port is the TCP port the platform listens on.
	Port int `json:"port"`
}

// Config is the platform's resolved runtime configuration: the deployment
// descriptor the platform booted with and its runtime parameters.
type Config struct {
	descriptor deployment.Descriptor
	runtime    Runtime
}

// configFile is the on-disk shape of platform.json: the descriptor fields at the
// top level (matching the deployment.json the builder writes) plus a runtime
// section.
type configFile struct {
	deployment.Descriptor
	Runtime Runtime `json:"runtime"`
}

// Load parses the embedded platform configuration into a Config. It fails fast
// on malformed embedded data.
func Load() (*Config, error) {
	return parse(platformJSON)
}

// parse decodes a platform configuration document and applies parameter
// defaults. It is separate from Load so the defaulting is testable without the
// embedded file.
func parse(data []byte) (*Config, error) {
	var f configFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("config: invalid platform configuration: %w", err)
	}
	rt := f.Runtime
	if rt.Port == 0 {
		rt.Port = defaultPort
	}
	return &Config{descriptor: f.Descriptor, runtime: rt}, nil
}

// Descriptor returns the deployment descriptor the platform booted with.
func (c *Config) Descriptor() deployment.Descriptor { return c.descriptor }

// Runtime returns the resolved runtime parameters.
func (c *Config) Runtime() Runtime { return c.runtime }

// Port returns the TCP port the platform listens on.
func (c *Config) Port() int { return c.runtime.Port }

// Summary renders the effective configuration as a human-readable block for
// logging at startup.
func (c *Config) Summary() string {
	d := c.descriptor
	var b strings.Builder
	fmt.Fprintf(&b, "platform configuration (machine=%s):\n", d.Machine)
	fmt.Fprintf(&b, "  deployment descriptor:\n")
	fmt.Fprintf(&b, "    platform     %s\n", d.Platform)
	fmt.Fprintf(&b, "    project      %s\n", d.Project)
	fmt.Fprintf(&b, "    environment  %s\n", d.Environment)
	fmt.Fprintf(&b, "    site         %s\n", d.Site)
	fmt.Fprintf(&b, "    machine      %s\n", d.Machine)
	fmt.Fprintf(&b, "    role         %s\n", d.Role)
	fmt.Fprintf(&b, "    ip           %s\n", d.IP)
	fmt.Fprintf(&b, "    services     %s\n", strings.Join(d.Services, ", "))
	fmt.Fprintf(&b, "    features     chaos=%t redundancy=%t\n", d.Features.Chaos, d.Features.Redundancy)
	fmt.Fprintf(&b, "  runtime parameters:\n")
	fmt.Fprintf(&b, "    port         %d", c.runtime.Port)
	return b.String()
}
