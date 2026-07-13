package deployment

import (
	"fmt"
	"sort"
	"strings"

	"github.com/miroslav-matejovsky/opdl/distribution/catalog"
)

// Descriptor is the per-machine deployment artifact: the resolved execution
// definition the builder produces (as JSON) and the platform runs from. It is
// not application configuration and not topology. It names one machine's role,
// the features turned on for it, the service kinds it hosts and their
// parameters, its backing store, and the Runtime policy that governs how local
// runtime configuration may adjust operational parameters. Together those define
// a deployment variant.
//
// The json struct tags are the on-disk wire format: the builder marshals a
// Descriptor to JSON and embeds it; the platform unmarshals it back. No HCL is
// involved at runtime.
type Descriptor struct {
	// Platform is the platform product line name this binary belongs to.
	Platform string `json:"platform"`
	// Project is the project (customer) identifier.
	Project string `json:"project"`
	// Environment is the target environment, e.g. "production".
	Environment string `json:"environment"`
	// Site is the site within the project this machine belongs to.
	Site string `json:"site"`
	// Machine is the machine identifier this binary is configured for.
	Machine string `json:"machine"`
	// Role is the machine's role, e.g. "sensor-node".
	Role string `json:"role"`

	// Features are the capability switches enabled for this machine.
	Features Features `json:"features"`
	// HostsAuthority reports whether this machine hosts the site authority role
	// (registry and configuration coordination). One authority is the default,
	// smallest deployment; in standalone mode the authority is embedded and local
	// with no LAN listener. Normal data nodes never become authority implicitly.
	HostsAuthority bool `json:"hosts_authority,omitempty"`
	// EnabledServices lists the service kinds this machine hosts. Every entry
	// must be a known catalog service and satisfy its feature gate.
	EnabledServices []string `json:"enabled_services"`
	// Services holds per-service configuration keyed by service kind.
	Services []ServiceConfig `json:"services,omitempty"`
	// Database is the backing store this machine connects to.
	Database Database `json:"database"`
	// Runtime declares the operational parameters the platform reads at startup
	// and the ownership/override policy for each.
	Runtime Runtime `json:"runtime"`
}

// Features are the capability switches for a machine.
type Features struct {
	OPCUA     bool `json:"opcua"`
	Alarms    bool `json:"alarms"`
	Recording bool `json:"recording"`
	Analytics bool `json:"analytics"`
	Historian bool `json:"historian"`
	Chaos     bool `json:"chaos"`
}

// Enabled reports whether the named feature key (see package catalog) is on.
// An unknown key is always false.
func (f Features) Enabled(key string) bool {
	switch key {
	case catalog.FeatureOPCUA:
		return f.OPCUA
	case catalog.FeatureAlarms:
		return f.Alarms
	case catalog.FeatureRecording:
		return f.Recording
	case catalog.FeatureAnalytics:
		return f.Analytics
	case catalog.FeatureHistorian:
		return f.Historian
	case catalog.FeatureChaos:
		return f.Chaos
	default:
		return false
	}
}

// ServiceConfig is per-service configuration for one hosted service kind.
type ServiceConfig struct {
	// Name is the service kind this configuration applies to.
	Name string `json:"name"`
	// Assignment is the stable assignment id for this service instance on the
	// machine. It is the source of the service's stable runtime instance id, so
	// the identity survives restarts and (later) same-kind duplicates on one
	// machine. The builder derives it; when empty, AssignmentID falls back to the
	// service kind, which is unique per machine today.
	Assignment string `json:"assignment,omitempty"`
	// Endpoint is a connection endpoint the service needs, e.g. an OPC UA URL.
	Endpoint string `json:"endpoint,omitempty"`
	// Namespace is an optional namespace/index qualifier for the endpoint.
	Namespace string `json:"namespace,omitempty"`
	// Interval is an optional poll/emit interval as a Go duration string.
	Interval string `json:"interval,omitempty"`
	// Redundancy fixes whether this service instance is active-active or belongs
	// to a static, fenced single-active group.
	Redundancy RedundancyPolicy `json:"redundancy,omitempty"`
}

// Database describes how a machine connects to its backing store.
type Database struct {
	Provider string `json:"provider"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
}

// LookupService returns the per-service configuration for a service kind.
func (d Descriptor) LookupService(name string) (ServiceConfig, bool) {
	for _, s := range d.Services {
		if s.Name == name {
			return s, true
		}
	}
	return ServiceConfig{}, false
}

// AssignmentID returns the stable assignment id for a service kind on this
// machine: the explicit ServiceConfig.Assignment when set, otherwise the kind
// itself (unique per machine today). It is the source of a service's stable
// runtime instance id.
func (d Descriptor) AssignmentID(kind string) string {
	if s, ok := d.LookupService(kind); ok && s.Assignment != "" {
		return s.Assignment
	}
	return kind
}

// Validate checks that a deployment descriptor is internally coherent: identity
// fields are set, every enabled service is a known catalog kind, no service is
// listed twice, each service satisfies its feature gate, and the runtime policy
// is well formed. It fails fast on the first problem so neither the build step
// nor the runtime proceeds on a broken deployment.
func (d Descriptor) Validate() error {
	if strings.TrimSpace(d.Platform) == "" {
		return fmt.Errorf("descriptor: platform is required")
	}
	if strings.TrimSpace(d.Project) == "" {
		return fmt.Errorf("descriptor: project is required")
	}
	if strings.TrimSpace(d.Environment) == "" {
		return fmt.Errorf("descriptor: environment is required")
	}
	if strings.TrimSpace(d.Machine) == "" {
		return fmt.Errorf("descriptor: machine is required")
	}
	if strings.TrimSpace(d.Role) == "" {
		return fmt.Errorf("descriptor: role is required")
	}

	seen := make(map[string]bool, len(d.EnabledServices))
	for _, name := range d.EnabledServices {
		svc, ok := catalog.LookupService(name)
		if !ok {
			return fmt.Errorf("descriptor: unknown service %q", name)
		}
		if seen[name] {
			return fmt.Errorf("descriptor: service %q enabled more than once", name)
		}
		seen[name] = true
		if svc.RequiredFeature != "" && !d.Features.Enabled(svc.RequiredFeature) {
			return fmt.Errorf("descriptor: service %q requires feature %q which is not enabled",
				name, svc.RequiredFeature)
		}
	}

	assignments := make(map[string]bool, len(d.Services))
	for _, s := range d.Services {
		if !seen[s.Name] {
			return fmt.Errorf("descriptor: per-service config for %q which is not an enabled service", s.Name)
		}
		if s.Assignment != "" {
			if assignments[s.Assignment] {
				return fmt.Errorf("descriptor: service assignment id %q used more than once", s.Assignment)
			}
			assignments[s.Assignment] = true
		}
		if err := s.Redundancy.Validate(); err != nil {
			return fmt.Errorf("descriptor: service %q: %w", s.Name, err)
		}
	}

	if strings.TrimSpace(d.Database.Provider) == "" {
		return fmt.Errorf("descriptor: database provider is required")
	}
	return d.Runtime.validate()
}

// Summary renders a compact, human-readable overview. Both the builder plan
// output and the platform startup log use it so the two sides describe a
// machine identically.
func (d Descriptor) Summary() string {
	services := append([]string(nil), d.EnabledServices...)
	sort.Strings(services)

	var features []string
	for _, key := range catalog.Features() {
		if d.Features.Enabled(key) {
			features = append(features, key)
		}
	}

	lines := []string{
		fmt.Sprintf("platform=%s", d.Platform),
		fmt.Sprintf("project=%s env=%s site=%s machine=%s role=%s authority=%t",
			d.Project, d.Environment, d.Site, d.Machine, d.Role, d.HostsAuthority),
		fmt.Sprintf("database=%s@%s:%d", d.Database.Provider, d.Database.Host, d.Database.Port),
		"features=[" + strings.Join(features, " ") + "]",
		"services=[" + strings.Join(services, " ") + "]",
	}
	return strings.Join(lines, "\n")
}
