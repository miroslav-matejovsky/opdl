package topology

import (
	"fmt"
	"strings"
	"time"

	"github.com/miroslav-matejovsky/opdl/distribution/catalog"
)

// The hcl struct tags map HCL attributes and blocks onto fields. The "label"
// tag captures a block's name label (the "customer-a" in project "customer-a").

// Project is the top of a topology: one project (customer), the environment and
// backing store common to it, and the sites and machines nested inside.
type Project struct {
	// Name is the project (customer) identifier.
	Name string `hcl:"name,label"`
	// Environment is the target environment, e.g. "production".
	Environment string `hcl:"environment"`
	// Database is the backing store all machines in the project connect to.
	Database Database `hcl:"database,block"`
	// Features are the capability switches available to the project's machines.
	Features Features `hcl:"features,block"`
	// Sites are the locations the project is deployed to.
	Sites []Site `hcl:"site,block"`
}

// Features are the project-level capability switches. A machine may only be
// assigned a service whose required feature is enabled here.
type Features struct {
	OPCUA     bool `hcl:"opcua,optional"`
	Alarms    bool `hcl:"alarms,optional"`
	Recording bool `hcl:"recording,optional"`
	Analytics bool `hcl:"analytics,optional"`
	Historian bool `hcl:"historian,optional"`
	Chaos     bool `hcl:"chaos,optional"`
}

// Enabled reports whether the named catalog feature key is switched on.
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

// Database is the backing store configuration.
type Database struct {
	Provider string `hcl:"provider"`
	Host     string `hcl:"host"`
	Port     int    `hcl:"port,optional"`
}

// Site is one location within a project holding a set of machines.
type Site struct {
	// Name is the site identifier, unique within its project.
	Name string `hcl:"name,label"`
	// Machines are the deployable units placed at this site.
	Machines []Machine `hcl:"machine,block"`
}

// Machine is one deployable unit: it becomes exactly one deployment descriptor
// downstream.
type Machine struct {
	// Name is the machine identifier, unique within its project.
	Name string `hcl:"name,label"`
	// Role is the machine's role, e.g. "sensor-node".
	Role string `hcl:"role"`
	// NodeEndpoint is static advertised host:port for the internal node protocol.
	// Empty keeps a standalone package; distributed blueprints set it so builder
	// can place it in authority roster data.
	NodeEndpoint string `hcl:"node_endpoint,optional"`
	// Authority reports whether this machine hosts the site authority role
	// (registry and configuration coordination). Optional; defaults false. One
	// authority is the smallest deployment.
	Authority bool `hcl:"authority,optional"`
	// Services lists the catalog service kinds assigned to this machine.
	Services []string `hcl:"services"`
	// Assignments holds optional per-service assignment parameters.
	Assignments []ServiceAssignment `hcl:"service,block"`
}

// LookupAssignment returns the assignment parameters for a service kind on the
// machine, if present.
func (m Machine) LookupAssignment(name string) (ServiceAssignment, bool) {
	for _, a := range m.Assignments {
		if a.Name == name {
			return a, true
		}
	}
	return ServiceAssignment{}, false
}

// ServiceAssignment is the authoring detail of one service assigned to a
// machine: the connection parameters an operator sets for it in the blueprint.
type ServiceAssignment struct {
	// Name is the service kind this assignment applies to.
	Name string `hcl:"name,label"`
	// Endpoint is a connection endpoint the service needs.
	Endpoint string `hcl:"endpoint,optional"`
	// Namespace is an optional qualifier for the endpoint.
	Namespace string `hcl:"namespace,optional"`
	// Interval is an optional poll/emit interval as a Go duration string.
	Interval string `hcl:"interval,optional"`
	// Redundancy optionally declares static activation policy for this service.
	// Omitted policy is active-active.
	Redundancy *RedundancyPolicy `hcl:"redundancy,block"`
}

// RedundancyPolicy is topology intent for a service's active-active or
// single-active execution. Builder resolves it to the deployment contract.
type RedundancyPolicy struct {
	// Mode is active-active or single-active.
	Mode string `hcl:"mode,optional"`
	// Group is a stable site-local election group name.
	Group string `hcl:"group,optional"`
	// Scope is site for Step 5 site-local coordination.
	Scope string `hcl:"scope,optional"`
	// Members lists machines statically eligible to own the group.
	Members []string `hcl:"members,optional"`
	// LeaseDuration bounds ownership without a renewal.
	LeaseDuration string `hcl:"lease_duration,optional"`
	// RetryInitial is first acquisition retry delay.
	RetryInitial string `hcl:"retry_initial,optional"`
	// RetryMax caps acquisition backoff.
	RetryMax string `hcl:"retry_max,optional"`
	// FailoverTimeout bounds failover observation.
	FailoverTimeout string `hcl:"failover_timeout,optional"`
	// DrainTimeout bounds graceful drain before release.
	DrainTimeout string `hcl:"drain_timeout,optional"`
}

// Validate checks a project against the model's rules and the shared catalog. It
// fails fast on the first violation so a broken topology never reaches the
// builder. The rules narrow top down: identity must be present, names unique,
// and every machine may only run catalog service kinds whose required feature is
// enabled on the project.
func (p *Project) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("project: name is required")
	}
	if strings.TrimSpace(p.Environment) == "" {
		return fmt.Errorf("project %q: environment is required", p.Name)
	}
	if strings.TrimSpace(p.Database.Provider) == "" {
		return fmt.Errorf("project %q: database provider is required", p.Name)
	}
	if strings.TrimSpace(p.Database.Host) == "" {
		return fmt.Errorf("project %q: database host is required", p.Name)
	}
	if len(p.Sites) == 0 {
		return fmt.Errorf("project %q: at least one site is required", p.Name)
	}

	siteNames := make(map[string]bool, len(p.Sites))
	machineNames := make(map[string]bool)
	for _, site := range p.Sites {
		if strings.TrimSpace(site.Name) == "" {
			return fmt.Errorf("project %q: site with empty name", p.Name)
		}
		if siteNames[site.Name] {
			return fmt.Errorf("project %q: duplicate site %q", p.Name, site.Name)
		}
		siteNames[site.Name] = true

		if len(site.Machines) == 0 {
			return fmt.Errorf("site %q: at least one machine is required", site.Name)
		}
		for _, machine := range site.Machines {
			if err := p.validateMachine(site, machine, machineNames); err != nil {
				return err
			}
		}
		if err := validateSiteRedundancy(site); err != nil {
			return fmt.Errorf("site %q: %w", site.Name, err)
		}
	}
	return nil
}

// validateMachine checks one machine's identity and service assignments. Machine
// names must be unique across the whole project because each machine is a
// distinct deployment identity.
func (p *Project) validateMachine(site Site, machine Machine, machineNames map[string]bool) error {
	if strings.TrimSpace(machine.Name) == "" {
		return fmt.Errorf("site %q: machine with empty name", site.Name)
	}
	if machineNames[machine.Name] {
		return fmt.Errorf("project %q: duplicate machine %q", p.Name, machine.Name)
	}
	machineNames[machine.Name] = true

	if strings.TrimSpace(machine.Role) == "" {
		return fmt.Errorf("machine %q: role is required", machine.Name)
	}
	if len(machine.Services) == 0 {
		return fmt.Errorf("machine %q: at least one service is required", machine.Name)
	}

	assigned := make(map[string]bool, len(machine.Services))
	for _, name := range machine.Services {
		svc, ok := catalog.LookupService(name)
		if !ok {
			return fmt.Errorf("machine %q: unknown service %q", machine.Name, name)
		}
		if assigned[name] {
			return fmt.Errorf("machine %q: service %q assigned more than once", machine.Name, name)
		}
		assigned[name] = true
		if svc.RequiredFeature != "" && !p.Features.Enabled(svc.RequiredFeature) {
			return fmt.Errorf("machine %q: service %q requires feature %q which project %q does not enable",
				machine.Name, name, svc.RequiredFeature, p.Name)
		}
	}

	for _, a := range machine.Assignments {
		if !assigned[a.Name] {
			return fmt.Errorf("machine %q: assignment for service %q which is not assigned", machine.Name, a.Name)
		}
		if err := validateRedundancyPolicy(a.Redundancy); err != nil {
			return fmt.Errorf("machine %q: service %q: %w", machine.Name, a.Name, err)
		}
	}
	return nil
}

func validateRedundancyPolicy(policy *RedundancyPolicy) error {
	if policy == nil || policy.Mode == "" || policy.Mode == "active-active" {
		if policy == nil {
			return nil
		}
		if policy.Group != "" || policy.Scope != "" || len(policy.Members) != 0 ||
			policy.LeaseDuration != "" || policy.RetryInitial != "" || policy.RetryMax != "" ||
			policy.FailoverTimeout != "" || policy.DrainTimeout != "" {
			return fmt.Errorf("active-active must not declare election settings")
		}
		return nil
	}
	if policy.Mode != "single-active" {
		return fmt.Errorf("mode must be active-active or single-active")
	}
	if policy.Group == "" {
		return fmt.Errorf("single-active group is required")
	}
	if policy.Scope != "site" {
		return fmt.Errorf("single-active scope must be site")
	}
	if len(policy.Members) < 2 {
		return fmt.Errorf("single-active group must declare at least two members")
	}
	seen := make(map[string]bool, len(policy.Members))
	for _, member := range policy.Members {
		if member == "" {
			return fmt.Errorf("single-active member is required")
		}
		if seen[member] {
			return fmt.Errorf("single-active member %q appears more than once", member)
		}
		seen[member] = true
	}
	lease, err := requiredDuration("lease_duration", policy.LeaseDuration)
	if err != nil {
		return err
	}
	retryInitial, err := requiredDuration("retry_initial", policy.RetryInitial)
	if err != nil {
		return err
	}
	retryMax, err := requiredDuration("retry_max", policy.RetryMax)
	if err != nil {
		return err
	}
	if retryInitial > retryMax {
		return fmt.Errorf("retry_initial must not exceed retry_max")
	}
	if retryInitial >= lease {
		return fmt.Errorf("retry_initial must be shorter than lease_duration")
	}
	if _, err := requiredDuration("failover_timeout", policy.FailoverTimeout); err != nil {
		return err
	}
	_, err = requiredDuration("drain_timeout", policy.DrainTimeout)
	return err
}

func requiredDuration(name, value string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if value == "" || err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return duration, nil
}

func validateSiteRedundancy(site Site) error {
	machines := make(map[string]Machine, len(site.Machines))
	for _, machine := range site.Machines {
		machines[machine.Name] = machine
	}
	for _, machine := range site.Machines {
		for _, assignment := range machine.Assignments {
			policy := assignment.Redundancy
			if policy == nil || policy.Mode != "single-active" {
				continue
			}
			if !contains(policy.Members, machine.Name) {
				return fmt.Errorf("machine %q: service %q is not in its single-active member list", machine.Name, assignment.Name)
			}
			for _, memberName := range policy.Members {
				member, ok := machines[memberName]
				if !ok {
					return fmt.Errorf("single-active group %q member %q is not in site", policy.Group, memberName)
				}
				if !contains(member.Services, assignment.Name) {
					return fmt.Errorf("single-active group %q member %q does not host service %q", policy.Group, memberName, assignment.Name)
				}
				peer, ok := member.LookupAssignment(assignment.Name)
				if !ok || peer.Redundancy == nil || !sameRedundancyPolicy(*policy, *peer.Redundancy) {
					return fmt.Errorf("single-active group %q member %q must declare matching policy", policy.Group, memberName)
				}
			}
		}
	}
	return nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func sameRedundancyPolicy(left, right RedundancyPolicy) bool {
	return left.Mode == right.Mode && left.Group == right.Group && left.Scope == right.Scope &&
		left.LeaseDuration == right.LeaseDuration && left.RetryInitial == right.RetryInitial &&
		left.RetryMax == right.RetryMax && left.FailoverTimeout == right.FailoverTimeout &&
		left.DrainTimeout == right.DrainTimeout && strings.Join(left.Members, "\x00") == strings.Join(right.Members, "\x00")
}
