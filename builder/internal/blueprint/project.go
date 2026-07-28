package blueprint

import (
	"fmt"
	"strings"
)

// Project is the top of a topology: one project (customer), the environment
// common to it, and the sites and machines nested inside.
type Project struct {
	// Name is the project (customer) identifier.
	Name string `hcl:"name,label"`
	// Environment is the target environment, e.g. "production".
	Environment string `hcl:"environment"`
	// Sites are the locations the project is deployed to.
	Sites []Site `hcl:"site,block"`
}

// Validate checks a project against the model's structural rules. It fails
// fast on the first violation. Identity must be present, and site, machine,
// and service names must be unique within their scope.
func (p *Project) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("project: name is required")
	}
	if strings.TrimSpace(p.Environment) == "" {
		return fmt.Errorf("project %q: environment is required", p.Name)
	}
	if len(p.Sites) == 0 {
		return fmt.Errorf("project %q: at least one site is required", p.Name)
	}

	siteNames := make(map[string]bool, len(p.Sites))
	clusterNames := make(map[string]string, len(p.Sites))
	machineNames := make(map[string]bool)
	machineIPs := make(map[string]string)
	for _, site := range p.Sites {
		if strings.TrimSpace(site.Name) == "" {
			return fmt.Errorf("project %q: site with empty name", p.Name)
		}
		if siteNames[site.Name] {
			return fmt.Errorf("project %q: duplicate site %q", p.Name, site.Name)
		}
		siteNames[site.Name] = true

		if err := validateSiteNATS(site); err != nil {
			return err
		}
		// Two sites naming one cluster is rejected rather than allowed to mean
		// two separate clusters that happen to share a name. Cluster membership
		// is what an embedded server checks before it accepts a route, so a
		// repeated name is the one thing that could let a route authored across
		// sites be accepted instead of refused.
		if owner, taken := clusterNames[site.ClusterName()]; taken {
			return fmt.Errorf("project %q: sites %q and %q both name their event fabric cluster %q; each site forms its own",
				p.Name, owner, site.Name, site.ClusterName())
		}
		clusterNames[site.ClusterName()] = site.Name

		for _, machine := range site.Machines {
			if err := p.validateMachine(site, machine, machineNames); err != nil {
				return err
			}
			if owner, taken := machineIPs[machine.IP]; taken {
				return fmt.Errorf("project %q: machines %q and %q share ip %q", p.Name, owner, machine.Name, machine.IP)
			}
			machineIPs[machine.IP] = machine.Name
		}
	}
	return nil
}
