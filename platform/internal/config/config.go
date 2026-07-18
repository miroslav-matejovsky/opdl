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
	address           string
	readHeaderTimeout time.Duration
	shutdownTimeout   time.Duration
	instanceDir       string
	lagBound          time.Duration
	eventFabric       EventFabric
	username          string
	password          string
}

// Load composes a Config from the platform's embedded deployment descriptor and
// the TOML configuration file at configPath. No defaults are allowed: the
// configuration file must exist and carry valid settings for all required options.
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
	if _, err := validateDuration("[event_fabric.nats] startup_timeout", f.EventFabric.Nats.StartupTimeout); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if _, err := validateDuration("[event_fabric.nats] catch_up_timeout", f.EventFabric.Nats.CatchUpTimeout); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	lagBound, err := validateLagBound(f.LagBound)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	cfg := &Config{
		descriptor:        d,
		address:           f.Address,
		readHeaderTimeout: readHeaderTimeout,
		shutdownTimeout:   shutdownTimeout,
		instanceDir:       f.InstanceDir,
		lagBound:          lagBound,
		eventFabric:       f.EventFabric,
	}
	// Credentials are read here rather than by the adapter: composition owns
	// files and secrets, and the adapter is handed values. A configured file that
	// cannot be read is a startup failure, not a surprise when a listener opens.
	if path := f.EventFabric.Nats.CredentialsFile; path != "" {
		site, err := loadCredentials(path)
		if err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
		cfg.username, cfg.password = site.Username, site.Password
	}
	return cfg, nil
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

// ReadHeaderTimeout returns the maximum duration allowed for reading HTTP request headers.
func (c *Config) ReadHeaderTimeout() time.Duration { return c.readHeaderTimeout }

// ShutdownTimeout returns the maximum duration allowed for graceful server and
// Event Fabric shutdown.
func (c *Config) ShutdownTimeout() time.Duration { return c.shutdownTimeout }

// InstanceDir returns the local runtime directory holding this machine's slot
// fence and per-slot status files. Both slots of a machine share it.
func (c *Config) InstanceDir() string { return c.instanceDir }

// LagBound returns the configured projection lag bound. A slot lagging beyond it
// is not promotable, and an active slot beyond it stops serving.
func (c *Config) LagBound() time.Duration { return c.lagBound }

// EventFabric returns the Event Fabric adapter settings from the configuration
// file. Only runtime composition reads it: it is how a site places the journal's
// storage and moves the transport's sockets, and no domain package has any
// business knowing a transport is configurable.
func (c *Config) EventFabric() EventFabric { return c.eventFabric }

// Credentials returns the site's NATS username and password, empty when no
// credentials file is configured. They are held apart from EventFabric so a
// secret is never carried in the struct the startup summary renders.
func (c *Config) Credentials() (username, password string) { return c.username, c.password }

// Summary renders the effective configuration as a human-readable block for
// logging at startup. It names the credentials file but never reads a secret
// into the log.
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
	fmt.Fprintf(&b, "    instances    warm_standby=%t\n", d.Instances.WarmStandby)
	fmt.Fprintf(&b, "    event_fabric %s\n", eventFabricSummary(d.EventFabric))
	fmt.Fprintf(&b, "  configuration file (TOML, user-provided):\n")
	fmt.Fprintf(&b, "    address             %s\n", c.address)
	fmt.Fprintf(&b, "    read_header_timeout %s\n", c.readHeaderTimeout)
	fmt.Fprintf(&b, "    shutdown_timeout    %s\n", c.shutdownTimeout)
	fmt.Fprintf(&b, "    instance_dir        %s\n", c.instanceDir)
	fmt.Fprintf(&b, "    lag_bound           %s\n", lagBoundSummary(c.lagBound))
	fmt.Fprintf(&b, "    event_fabric.nats   %s", natsSummary(c.eventFabric.Nats))
	return b.String()
}

// eventFabricSummary renders the derived Event Fabric membership: which peers
// this machine expects to meet on the site's journal.
func eventFabricSummary(f deployment.EventFabric) string {
	if len(f.Peers) == 0 {
		return "one-member site"
	}
	peers := make([]string, 0, len(f.Peers))
	for _, peer := range f.Peers {
		peers = append(peers, fmt.Sprintf("%s (%s)", peer.Machine, peer.IP))
	}
	return fmt.Sprintf("peers: %s", strings.Join(peers, ", "))
}

// natsSummary renders the Event Fabric adapter's settings, so a startup log
// shows where the journal is stored and whether a machine is running on its
// deployment addresses or on local ones.
//
// It names the credentials file and never renders its content: a startup block
// is copied into tickets and chat windows, so a secret must not be able to reach
// it in the first place.
func natsSummary(n EventFabricNats) string {
	parts := []string{
		"data_dir=" + n.DataDir,
		"startup_timeout=" + n.StartupTimeout,
		"catch_up_timeout=" + n.CatchUpTimeout,
		"credentials_file=" + credentialsSummary(n.CredentialsFile),
	}
	if n.ClientAddress != "" {
		parts = append(parts, "client="+n.ClientAddress)
	}
	if n.ClusterAddress != "" {
		parts = append(parts, "cluster="+n.ClusterAddress)
	}
	if n.MonitorAddress != "" {
		parts = append(parts, "monitor="+n.MonitorAddress)
	}
	if n.Routes != nil {
		parts = append(parts, "routes="+strings.Join(n.Routes, ","))
	}
	if n.Servers != nil {
		parts = append(parts, "servers="+strings.Join(n.Servers, ","))
	}
	return strings.Join(parts, " ")
}

// lagBoundSummary renders the required projection lag bound.
func lagBoundSummary(bound time.Duration) string { return bound.String() }

// credentialsSummary renders an unset credentials file as an explicit statement
// that the deployment is running unauthenticated, so the startup block never
// shows a blank value.
func credentialsSummary(path string) string {
	if path == "" {
		return "(none: loopback only)"
	}
	return path
}
