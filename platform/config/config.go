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
	eventFabric       EventFabric
	username          string
	password          string
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
	lagBound, err := eventStorageSettings(configPath, d, f)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	cfg := &Config{
		descriptor:        d,
		readHeaderTimeout: readHeaderTimeout,
		shutdownTimeout:   shutdownTimeout,
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

// eventStorageSettings validates the settings that only mean something to a
// deployment with a site journal, and returns the projection lag bound.
//
// lag_bound bounds how far a projection may fall behind the journal, and the two
// [event_fabric.nats] timeouts bound starting a server and catching up on it.
// A deployment with no event storage has no journal, no projection, and no
// server, so all three are settings with nothing to bound. Requiring them there
// would make an operator write three durations that no code path reads, which is
// worse than an absent value: it reads like configuration that is in effect.
//
// They stay strictly required wherever they do apply, and a value that is stated
// is validated whether it applies or not. Nothing here is defaulted: a
// deployment that has a journal must still say what its bounds are.
func eventStorageSettings(path string, d Descriptor, f file) (lagBound time.Duration, err error) {
	if !d.HasEventStorage() {
		// A stated value is still checked, so a file carried over from a
		// deployment that had a journal fails on a typo rather than being
		// silently ignored.
		for _, stated := range []struct{ name, value string }{
			{"lag_bound", f.LagBound},
			{"[event_fabric.nats] startup_timeout", f.EventFabric.Nats.StartupTimeout},
			{"[event_fabric.nats] catch_up_timeout", f.EventFabric.Nats.CatchUpTimeout},
		} {
			if stated.value == "" {
				continue
			}
			if _, err := validateDuration(stated.name, stated.value); err != nil {
				return 0, err
			}
		}
		return 0, nil
	}

	if f.LagBound == "" {
		return 0, fmt.Errorf("configuration file %s: lag_bound is required", path)
	}
	if f.EventFabric.Nats.StartupTimeout == "" {
		return 0, fmt.Errorf("configuration file %s: [event_fabric.nats] startup_timeout is required", path)
	}
	if f.EventFabric.Nats.CatchUpTimeout == "" {
		return 0, fmt.Errorf("configuration file %s: [event_fabric.nats] catch_up_timeout is required", path)
	}
	if _, err := validateDuration("[event_fabric.nats] startup_timeout", f.EventFabric.Nats.StartupTimeout); err != nil {
		return 0, err
	}
	if _, err := validateDuration("[event_fabric.nats] catch_up_timeout", f.EventFabric.Nats.CatchUpTimeout); err != nil {
		return 0, err
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
	fmt.Fprintf(&b, "    features     chaos=%t\n", d.Features.Chaos)
	fmt.Fprintf(&b, "    instances    %s\n", instancesSummary(d.Instances, Role(standby)))
	fmt.Fprintf(&b, "    data_dir     %s\n", optionalPathSummary(inst.DataDir))
	fmt.Fprintf(&b, "    event_storage %s\n", eventStorageSummary(inst.Nats))
	fmt.Fprintf(&b, "    lock         %s\n", lockSummary(d.Lock))
	fmt.Fprintf(&b, "    peers        %s\n", peersSummary(d.Peers))
	fmt.Fprintf(&b, "  configuration file (TOML, user-provided):\n")
	fmt.Fprintf(&b, "    read_header_timeout %s\n", c.readHeaderTimeout)
	fmt.Fprintf(&b, "    shutdown_timeout    %s\n", c.shutdownTimeout)
	// The journal's bounds are printed only by a deployment that has one. Showing
	// "lag_bound 0s" and a line of empty timeouts on a deployment with no event
	// storage would read as a misconfiguration rather than as three settings that
	// do not apply.
	if d.HasEventStorage() {
		fmt.Fprintf(&b, "    lag_bound           %s\n", lagBoundSummary(c.lagBound))
		fmt.Fprintf(&b, "    event_fabric.nats   %s", natsSummary(c.eventFabric.Nats))
	} else {
		fmt.Fprint(&b, "    lag_bound, event_fabric.nats  (not applicable: no event storage)")
	}
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

// lockSummary renders the Windows named mutex when a standby is deployed.
func lockSummary(lock *Lock) string {
	if lock == nil {
		return "(not deployed)"
	}
	return lock.WindowsMutex
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

// natsSummary renders the Event Fabric adapter's settings a site owns: its
// startup bounds and where its transport credentials are read from.
//
// Where the journal is stored is no longer among them. It is the instance's own
// and is rendered from the descriptor above, because a machine's two instances
// open two stores.
//
// It names the credentials file and never renders its content: a startup block
// is copied into tickets and chat windows, so a secret must not be able to reach
// it in the first place.
func natsSummary(n EventFabricNats) string {
	return strings.Join([]string{
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
		return "(not configured)"
	}
	return path
}

// eventStorageSummary renders where this instance's journal lives, and says so
// plainly when the deployment has none.
//
// The distinction is worth a sentence rather than an empty value: an instance
// with no event storage is not misconfigured, it is a deployment that serves no
// domain operations, and an operator reading the startup block should not have
// to infer that from a blank path.
func eventStorageSummary(nats *Nats) string {
	if nats == nil {
		return "(none: this deployment has no event storage and serves no domain operations)"
	}
	return optionalPathSummary(nats.JetStreamStoreDir)
}
