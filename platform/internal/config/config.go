package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/platform/embedded"
)

// Config is the platform's resolved runtime configuration: a composition of the
// deployment descriptor (see package embedded, which the builder stages before
// compiling) and the platform's TOML configuration file, which supplies settings
// a user must explicitly specify without rebuilding the binary.
// Everything an instance binds or writes on its own is read from the descriptor
// by role, not held here: the configuration file is what a site decides for the
// machine, and a machine's two instances read the same copy of it.
type Config struct {
	descriptor        deployment.Descriptor
	readHeaderTimeout time.Duration
	shutdownTimeout   time.Duration
	lagBound          time.Duration
	operations        Operations
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
		readHeaderTimeout: readHeaderTimeout,
		shutdownTimeout:   shutdownTimeout,
		lagBound:          lagBound,
		operations:        f.Operations,
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

// Descriptor returns the deployment descriptor the platform booted with.
func (c *Config) Descriptor() deployment.Descriptor { return c.descriptor }

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

// OperationsEventDir returns the optional directory for append-only JSONL
// operational events. An empty path disables file retention, not stderr events.
func (c *Config) OperationsEventDir() string { return c.operations.EventDir }

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
//
// It takes the running instance's role so a two-instance machine's two startup
// blocks are told apart: both list the same descriptor and the same
// configuration file, and the only thing that differs is which instance printed
// it.
func (c *Config) Summary(standby bool) string {
	d := c.descriptor
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
	fmt.Fprintf(&b, "    features     chaos=%t\n", d.Features.Chaos)
	fmt.Fprintf(&b, "    instances    %s\n", instancesSummary(d.Instances, deployment.Role(standby)))
	// A machine's primary and standby processes contend for one Windows named
	// mutex, so printing it at startup is how an operator finds which object they
	// contend for.
	fmt.Fprintf(&b, "    lock         %s\n", lockSummary(d.Lock))
	fmt.Fprintf(&b, "    peers        %s\n", peersSummary(d.Peers))
	fmt.Fprintf(&b, "  configuration file (TOML, user-provided):\n")
	fmt.Fprintf(&b, "    read_header_timeout %s\n", c.readHeaderTimeout)
	fmt.Fprintf(&b, "    shutdown_timeout    %s\n", c.shutdownTimeout)
	fmt.Fprintf(&b, "    lag_bound           %s\n", lagBoundSummary(c.lagBound))
	fmt.Fprintf(&b, "    operations.event_dir %s\n", optionalPathSummary(c.operations.EventDir))
	fmt.Fprintf(&b, "    event_fabric.nats   %s", natsSummary(c.eventFabric.Nats))
	return b.String()
}

// instancesSummary renders which of the machine's two instances are deployed,
// where each serves its API, and which one is reading this block.
//
// Both endpoints are stated whichever instance printed it, because an operator
// looking at one instance's log is usually trying to find the other. The marker
// on self is what keeps the two logs of one machine from being identical.
func instancesSummary(instances deployment.Instances, self deployment.PlatformInstanceRole) string {
	parts := make([]string, 0, 2)
	for _, role := range []deployment.PlatformInstanceRole{deployment.RolePrimary, deployment.RoleStandby} {
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

// lockSummary renders the Windows named mutex when a standby is deployed.
func lockSummary(lock *deployment.Lock) string {
	if lock == nil {
		return "(not deployed)"
	}
	return lock.WindowsMutex
}

// peersSummary renders the site's membership: the platform instances this
// machine expects to meet on the site's journal, its own included.
func peersSummary(peers []deployment.Peer) string {
	if len(peers) == 0 {
		return "(none resolved)"
	}
	names := make([]string, 0, len(peers))
	for _, peer := range peers {
		names = append(names, fmt.Sprintf("%s/%s (%s)", peer.Machine, peer.Role, peer.IP))
	}
	return strings.Join(names, ", ")
}

// natsSummary renders the Event Fabric adapter's settings, so a startup log
// shows where the journal is stored and whether a machine is running on its
// deployment addresses or on local ones.
//
// It names the credentials file and never renders its content: a startup block
// is copied into tickets and chat windows, so a secret must not be able to reach
// it in the first place.
func natsSummary(n EventFabricNats) string {
	return strings.Join([]string{
		"data_dir=" + n.DataDir,
		"startup_timeout=" + n.StartupTimeout,
		"catch_up_timeout=" + n.CatchUpTimeout,
		"credentials_file=" + credentialsSummary(n.CredentialsFile),
	}, " ")
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

func optionalPathSummary(path string) string {
	if path == "" {
		return "(disabled; JSON events remain on stderr)"
	}
	return path
}
