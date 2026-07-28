package config

import (
	"fmt"
	"strings"
	"time"
)

// Config is the platform's effective runtime configuration: the deployment
// descriptor the builder stages before compiling (see Deployment), with every
// duration it carries parsed once at startup.
//
// There is one source. Everything an instance binds, writes, or is bounded by is
// authored in the project blueprint and resolved onto that instance's record in
// the descriptor, so a binary states its own configuration and a site changes it
// by rebuilding rather than by editing a file next to the executable.
type Config struct {
	descriptor Descriptor
	primary    instanceTimeouts
	standby    instanceTimeouts
	lagBound   time.Duration
}

// instanceTimeouts are one instance's parsed listener timeouts. They are per
// instance because the listener they govern is: a machine's two instances are
// independent runtimes that bind their own addresses.
type instanceTimeouts struct {
	readHeader time.Duration
	shutdown   time.Duration
}

// Load composes a Config from the platform's embedded deployment descriptor. No
// defaults are applied: a descriptor missing a duration, or carrying one that is
// not positive, is a startup failure rather than a value someone has to guess at
// later.
func Load() (*Config, error) {
	d, err := Deployment()
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	cfg := &Config{descriptor: d}
	cfg.primary, err = timeoutsOf(RolePrimary, &d.Primary)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	cfg.standby, err = timeoutsOf(RoleStandby, d.Standby)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	cfg.lagBound, err = lagBoundOf(d.Lease)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}

// timeoutsOf parses one instance's listener timeouts. An instance that is not
// deployed has no record and binds nothing, so its timeouts stay zero and are
// never read.
func timeoutsOf(role PlatformInstanceRole, instance *Instance) (instanceTimeouts, error) {
	if instance == nil {
		return instanceTimeouts{}, nil
	}
	readHeader, err := validateDuration(fmt.Sprintf("instances.%s.api_read_header_timeout", role), instance.APIReadHeaderTimeout)
	if err != nil {
		return instanceTimeouts{}, err
	}
	shutdown, err := validateDuration(fmt.Sprintf("instances.%s.api_shutdown_timeout", role), instance.APIShutdownTimeout)
	if err != nil {
		return instanceTimeouts{}, err
	}
	return instanceTimeouts{readHeader: readHeader, shutdown: shutdown}, nil
}

// lagBoundOf parses the lease's projection lag bound. A machine that deploys no
// Standby Instance carries no lease: it trades ownership with nobody, so there
// is no failover for a lag bound to gate and the bound is zero.
func lagBoundOf(lease *Lease) (time.Duration, error) {
	if lease == nil {
		return 0, nil
	}
	return validateDuration("lease.lag_bound", lease.LagBound)
}

func validateDuration(name, s string) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", name, s, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("invalid %s %s: duration must be positive", name, d)
	}
	return d, nil
}

// Descriptor returns the deployment descriptor the platform booted with.
func (c *Config) Descriptor() Descriptor { return c.descriptor }

// timeouts returns one instance's parsed listener timeouts.
func (c *Config) timeouts(standby bool) instanceTimeouts {
	if standby {
		return c.standby
	}
	return c.primary
}

// ReadHeaderTimeout returns the running instance's maximum duration for reading
// HTTP request headers. It takes the instance role because the two instances of
// a machine bind their own listeners and each is bounded by its own authored
// value.
func (c *Config) ReadHeaderTimeout(standby bool) time.Duration {
	return c.timeouts(standby).readHeader
}

// ShutdownTimeout returns the running instance's maximum duration for the
// graceful drain of its listener.
func (c *Config) ShutdownTimeout(standby bool) time.Duration {
	return c.timeouts(standby).shutdown
}

// LagBound returns the lease's projection lag bound. A process lagging beyond it
// is not ready to take over, and an active process beyond it stops serving. It
// is zero on a machine that deploys no Standby Instance and therefore no lease.
func (c *Config) LagBound() time.Duration { return c.lagBound }

// Summary renders the effective configuration as a human-readable block for
// logging at startup.
//
// It takes the running instance's role so a two-instance machine's two startup
// blocks are told apart: both list the same descriptor, and what differs is
// which instance printed it and which listener timeouts that instance is bound
// by.
func (c *Config) Summary(standby bool) string {
	d := c.descriptor
	inst := d.Instance(Role(standby))
	timeouts := c.timeouts(standby)
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
	fmt.Fprintf(&b, "    instances    %s\n", instancesSummary(d, Role(standby)))
	fmt.Fprintf(&b, "    events_file  %s\n", optionalPathSummary(inst.EventsFile))
	fmt.Fprintf(&b, "    state_file   %s\n", optionalPathSummary(inst.StateFile))
	fmt.Fprintf(&b, "    log_file     %s\n", optionalPathSummary(inst.LogFile))
	// The machine's own store is printed after this instance's two files and
	// before the lease, which is the order they belong to: two an instance owns,
	// then two the machine's instances share.
	fmt.Fprintf(&b, "    machine_events_file %s\n", optionalPathSummary(d.MachineEventsFile))
	fmt.Fprintf(&b, "    lease        %s\n", leaseSummary(d.Lease))
	fmt.Fprintf(&b, "  this instance's api:\n")
	fmt.Fprintf(&b, "    read_header_timeout %s\n", timeouts.readHeader)
	fmt.Fprintf(&b, "    shutdown_timeout    %s\n", timeouts.shutdown)
	// The embedded event fabric server is this instance's too, and it is the
	// other listener the process binds, so it is printed beside the API rather
	// than with the descriptor block above.
	fmt.Fprintf(&b, "  this instance's event fabric:\n")
	fmt.Fprintf(&b, "    nats_server_name     %s\n", inst.NATS.ServerName)
	fmt.Fprintf(&b, "    nats_cluster_name    %s\n", inst.NATS.ClusterName)
	fmt.Fprintf(&b, "    nats_cluster_address %s\n", inst.NATS.ClusterAddress)
	fmt.Fprintf(&b, "    nats_routes          %s\n", routesSummary(inst.NATS.Routes))
	return b.String()
}

// routesSummary renders the peers this instance's embedded server routes to.
//
// An empty list is printed as a statement rather than as a blank, because on a
// site that deploys one instance it is the correct answer and an operator
// reading a blank line would have no way to tell that from a truncated
// descriptor.
func routesSummary(routes []string) string {
	if len(routes) == 0 {
		return "(none; this instance is the only one at its site)"
	}
	return strings.Join(routes, " ")
}

// instancesSummary renders which of the machine's two instances are deployed,
// where each serves its API, and which one is reading this block.
//
// Both endpoints are stated whichever instance printed it, because an operator
// looking at one instance's log is usually trying to find the other. The marker
// on self is what keeps the two logs of one machine from being identical.
func instancesSummary(d Descriptor, self PlatformInstanceRole) string {
	parts := make([]string, 0, 2)
	for _, role := range []PlatformInstanceRole{RolePrimary, RoleStandby} {
		if role == RoleStandby && !d.HasStandby() {
			parts = append(parts, fmt.Sprintf("%s=(not deployed)", role))
			continue
		}
		instance := d.Instance(role)
		marker := ""
		if role == self {
			marker = " (this instance)"
		}
		parts = append(parts, fmt.Sprintf("%s=%s%s", role, instance.APIAddress, marker))
	}
	return strings.Join(parts, " ")
}

// leaseSummary renders the Primary Ownership lease file and its timings when a
// standby is deployed. The lag bound is here because it is a lease timing: it
// bounds whether ownership may move at all.
func leaseSummary(lease *Lease) string {
	if lease == nil {
		return "(not deployed)"
	}
	return fmt.Sprintf("%s duration=%s renewal=%s health_check=%s failback=%s lag_bound=%s",
		lease.File, lease.Duration, lease.RenewalInterval, lease.HealthCheckInterval, lease.FailbackStabilization, lease.LagBound)
}

func optionalPathSummary(path string) string {
	if path == "" {
		return "(not configured)"
	}
	return path
}
