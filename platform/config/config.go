package config

import (
	"fmt"
	"strings"
	"time"
)

// Config is the platform's effective runtime configuration: a composition of the
// deployment descriptor the builder stages before compiling (see Deployment) and
// the platform's TOML configuration file, which supplies the settings a site may
// specify without rebuilding the binary.
// Everything an instance binds or writes on its own is read from the descriptor
// by role, not held here: the configuration file is what a site decides for the
// machine, and a machine's two instances read the same copy of it.
type Config struct {
	descriptor        Descriptor
	readHeaderTimeout time.Duration
	shutdownTimeout   time.Duration
	lagBound          time.Duration
}

// Load composes a Config from the platform's embedded deployment descriptor and
// the TOML configuration file at configPath. No defaults are allowed: the
// configuration file must exist and carry valid settings for all required options.
func Load(configPath string) (*Config, error) {
	d, err := Deployment()
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
	lagBound, err := eventStorageSettings(configPath, f)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	cfg := &Config{
		descriptor:        d,
		readHeaderTimeout: readHeaderTimeout,
		shutdownTimeout:   shutdownTimeout,
		lagBound:          lagBound,
	}
	return cfg, nil
}

// eventStorageSettings validates the settings that only mean something to a
// deployment with a site journal, and returns the projection lag bound.
func eventStorageSettings(path string, f file) (lagBound time.Duration, err error) {
	if f.LagBound == "" {
		return 0, fmt.Errorf("configuration file %s: lag_bound is required", path)
	}
	return validateLagBound(f.LagBound)
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

// validateLagBound parses the required positive projection lag bound.
func validateLagBound(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("lag_bound is required")
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid lag_bound %q: %w", s, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("invalid lag_bound %s: duration must be positive", d)
	}
	return d, nil
}

// Descriptor returns the deployment descriptor the platform booted with.
func (c *Config) Descriptor() Descriptor { return c.descriptor }

// Nothing here answers for a single instance. The API address and the runtime
// directory are resolved onto each instance's record in the descriptor, and the
// runtime reads its own from there; see app.instanceOf. A Config accessor taking
// a role would be a second way to reach the same field, and the one the caller
// picked would be the one that could be wrong.

// ReadHeaderTimeout returns the maximum duration allowed for reading HTTP request headers.
func (c *Config) ReadHeaderTimeout() time.Duration { return c.readHeaderTimeout }

// ShutdownTimeout returns the maximum duration allowed for graceful server and
// Event Fabric shutdown.
func (c *Config) ShutdownTimeout() time.Duration { return c.shutdownTimeout }

// LagBound returns the configured projection lag bound. A process lagging beyond
// it is not ready to take over, and an active process beyond it stops serving.
func (c *Config) LagBound() time.Duration { return c.lagBound }

// Summary renders the effective configuration as a human-readable block for
// logging at startup. It names the credentials file but never reads a secret
// into the log.
//
// It takes the running instance's role so a two-instance machine's two startup
// blocks are told apart: both list the same descriptor and the same
// configuration file, and the only thing that differs is which instance printed
// it.
func (c *Config) Summary(standby bool) string {
	d := c.descriptor
	inst := d.Instances.Get(Role(standby))
	var b strings.Builder
	fmt.Fprintf(&b, "platform configuration (machine=%s):\n", d.Machine)
	fmt.Fprintf(&b, "  deployment descriptor (embedded, staged by builder):\n")
	fmt.Fprintf(&b, "    platform     %s\n", d.Platform)
	fmt.Fprintf(&b, "    project      %s\n", d.Project)
	fmt.Fprintf(&b, "    environment  %s\n", d.Environment)
	fmt.Fprintf(&b, "    site         %s\n", d.Site)
	fmt.Fprintf(&b, "    machine      %s\n", d.Machine)
	fmt.Fprintf(&b, "    profile      %s\n", d.MachineProfile)
	fmt.Fprintf(&b, "    ip           %s\n", d.IP)
	fmt.Fprintf(&b, "    services     %s\n", strings.Join(d.Services, ", "))
	fmt.Fprintf(&b, "    instances    %s\n", instancesSummary(d.Instances, Role(standby)))
	fmt.Fprintf(&b, "    data_dir     %s\n", optionalPathSummary(inst.DataDir))
	fmt.Fprintf(&b, "    lease        %s\n", leaseSummary(d.Lease))
	fmt.Fprintf(&b, "    peers        %s\n", peersSummary(d.Peers))
	fmt.Fprintf(&b, "  configuration file (TOML, user-provided):\n")
	fmt.Fprintf(&b, "    read_header_timeout %s\n", c.readHeaderTimeout)
	fmt.Fprintf(&b, "    shutdown_timeout    %s\n", c.shutdownTimeout)
	fmt.Fprintf(&b, "    lag_bound           %s\n", lagBoundSummary(c.lagBound))
	return b.String()
}

// instancesSummary renders which of the machine's two instances are deployed,
// where each serves its API, and which one is reading this block.
//
// Both endpoints are stated whichever instance printed it, because an operator
// looking at one instance's log is usually trying to find the other. The marker
// on self is what keeps the two logs of one machine from being identical.
func instancesSummary(instances Instances, self PlatformInstanceRole) string {
	parts := make([]string, 0, 2)
	for _, role := range []PlatformInstanceRole{RolePrimary, RoleStandby} {
		instance := instances.Get(role)
		if instance.Disabled {
			parts = append(parts, fmt.Sprintf("%s=(not deployed)", role))
			continue
		}
		marker := ""
		if role == self {
			marker = " (this instance)"
		}
		parts = append(parts, fmt.Sprintf("%s=%s%s", role, instance.APIAddress, marker))
	}
	return strings.Join(parts, " ")
}

// leaseSummary renders the Primary Ownership lease file and its timings when a
// standby is deployed.
func leaseSummary(lease *Lease) string {
	if lease == nil {
		return "(not deployed)"
	}
	return fmt.Sprintf("%s duration=%s renewal=%s health_check=%s failback=%s",
		lease.File, lease.Duration, lease.RenewalInterval, lease.HealthCheckInterval, lease.FailbackStabilization)
}

// peersSummary renders the site's membership: the platform instances this
// machine expects to meet on the site's journal, its own included.
func peersSummary(peers []Peer) string {
	if len(peers) == 0 {
		return "(none resolved)"
	}
	names := make([]string, 0, len(peers))
	for _, peer := range peers {
		names = append(names, fmt.Sprintf("%s/%s (%s)", peer.Machine, peer.Role, peer.IP))
	}
	return strings.Join(names, ", ")
}

// lagBoundSummary renders the required projection lag bound.
func lagBoundSummary(bound time.Duration) string { return bound.String() }

func optionalPathSummary(path string) string {
	if path == "" {
		return "(not configured)"
	}
	return path
}
