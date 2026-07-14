package resolve

import (
	"fmt"

	"github.com/miroslav-matejovsky/opdl/distribution/deployment"
	"github.com/miroslav-matejovsky/opdl/distribution/topology"
)

// Plan is the full set of per-machine deployment descriptors derived from a
// project's topology.
type Plan struct {
	Project  string
	Machines []deployment.Descriptor
	Rosters  map[string]deployment.SiteRoster
}

// Build translates a validated topology into a Plan of deployment descriptors,
// one per machine. platformName identifies the product line the machines are
// built from; it is supplied by builder rather than project topology. Each descriptor is
// validated here, so the builder fails before compiling a machine that would not
// boot.
func Build(p *topology.Project, platformName string) (*Plan, error) {
	plan := &Plan{Project: p.Name, Rosters: make(map[string]deployment.SiteRoster, len(p.Sites))}
	for _, site := range p.Sites {
		roster := siteRoster(p, site)
		if err := roster.Validate(); err != nil {
			return nil, err
		}
		plan.Rosters[site.Name] = roster
		for _, machine := range site.Machines {
			d := descriptor(p, site, machine, platformName)
			if err := d.Validate(); err != nil {
				return nil, fmt.Errorf("machine %q: %w", machine.Name, err)
			}
			plan.Machines = append(plan.Machines, d)
		}
	}
	return plan, nil
}

// Roster returns static service roster for one descriptor's authority scope.
func (p *Plan) Roster(d deployment.Descriptor) deployment.SiteRoster {
	return p.Rosters[d.Site]
}

// siteRoster resolves one site's expected service instances. Endpoint values are
// authored bootstrap data; runtime never learns or changes them.
func siteRoster(p *topology.Project, site topology.Site) deployment.SiteRoster {
	r := deployment.SiteRoster{Project: p.Name, Environment: p.Environment, Site: site.Name}
	for _, machine := range site.Machines {
		for _, service := range machine.Services {
			r.Services = append(r.Services, deployment.RosterService{
				Service:  service,
				Instance: service + "@" + machine.Name,
				Machine:  machine.Name,
				Node:     machine.Name,
				Role:     machine.Role,
				Endpoint: machine.NodeEndpoint,
				Redundancy: func() deployment.RedundancyPolicy {
					assignment, ok := machine.LookupAssignment(service)
					if !ok {
						return deployment.RedundancyPolicy{}
					}
					return redundancyPolicy(assignment.Redundancy)
				}(),
			})
		}
	}
	return r
}

// descriptor builds one machine's deployment descriptor from its place in the
// topology. This is where the layered topology collapses into a concrete,
// per-machine execution definition.
func descriptor(
	p *topology.Project,
	site topology.Site,
	machine topology.Machine,
	platformName string,
) deployment.Descriptor {
	services := make([]deployment.ServiceConfig, 0, len(machine.Assignments))
	for _, a := range machine.Assignments {
		services = append(services, deployment.ServiceConfig{
			Name: a.Name,
			// One service per kind per machine today, so the kind is a stable,
			// unique assignment id. Making it explicit fixes the identity now, so
			// later same-kind duplicates only extend this derivation.
			Assignment: a.Name,
			Endpoint:   a.Endpoint,
			Namespace:  a.Namespace,
			Interval:   a.Interval,
			Redundancy: redundancyPolicy(a.Redundancy),
		})
	}

	return deployment.Descriptor{
		Platform:       platformName,
		Project:        p.Name,
		Environment:    p.Environment,
		Site:           site.Name,
		Machine:        machine.Name,
		Role:           machine.Role,
		HostsAuthority: machine.Authority,
		Features: deployment.Features{
			OPCUA:     p.Features.OPCUA,
			Alarms:    p.Features.Alarms,
			Recording: p.Features.Recording,
			Analytics: p.Features.Analytics,
			Historian: p.Features.Historian,
			Chaos:     p.Features.Chaos,
		},
		EnabledServices: machine.Services,
		Services:        services,
		Database: deployment.Database{
			Provider: p.Database.Provider,
			Host:     p.Database.Host,
			Port:     p.Database.Port,
		},
		Runtime: deployment.DefaultRuntime(),
	}
}

func redundancyPolicy(policy *topology.RedundancyPolicy) deployment.RedundancyPolicy {
	if policy == nil {
		return deployment.RedundancyPolicy{}
	}
	return deployment.RedundancyPolicy{
		Mode:            deployment.RedundancyMode(policy.Mode),
		Group:           policy.Group,
		Scope:           policy.Scope,
		Members:         append([]string(nil), policy.Members...),
		LeaseDuration:   policy.LeaseDuration,
		RetryInitial:    policy.RetryInitial,
		RetryMax:        policy.RetryMax,
		FailoverTimeout: policy.FailoverTimeout,
		DrainTimeout:    policy.DrainTimeout,
	}
}
