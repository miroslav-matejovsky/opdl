package deployment

import (
	"fmt"
	"sort"
	"strings"
)

// SiteRoster is builder output for one authority scope. It is embedded only in
// authority packages. It fixes service identity, host placement, and ordered
// static endpoints before runtime starts.
type SiteRoster struct {
	Project     string          `json:"project"`
	Environment string          `json:"environment"`
	Site        string          `json:"site"`
	Services    []RosterService `json:"services"`
}

// RosterService is one expected service instance in an authority scope.
type RosterService struct {
	Service   string `json:"service"`
	Instance  string `json:"instance"`
	Machine   string `json:"machine"`
	Node      string `json:"node"`
	Role      string `json:"role"`
	Endpoint  string `json:"endpoint,omitempty"`
	Secondary string `json:"secondary_endpoint,omitempty"`
	// Redundancy is static activation policy for this instance. Authority uses it
	// to admit only declared group members to a fenced lease.
	Redundancy RedundancyPolicy `json:"redundancy,omitempty"`
}

// Validate checks roster identity and unique static service instances.
func (r SiteRoster) Validate() error {
	for _, field := range []struct{ name, value string }{
		{"project", r.Project}, {"environment", r.Environment}, {"site", r.Site},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("site roster: %s is required", field.name)
		}
	}
	seen := make(map[string]bool, len(r.Services))
	for _, s := range r.Services {
		for _, field := range []struct{ name, value string }{
			{"service", s.Service}, {"instance", s.Instance}, {"machine", s.Machine}, {"node", s.Node}, {"role", s.Role},
		} {
			if strings.TrimSpace(field.value) == "" {
				return fmt.Errorf("site roster: service %q %s is required", s.Instance, field.name)
			}
		}
		if seen[s.Instance] {
			return fmt.Errorf("site roster: service instance %q appears more than once", s.Instance)
		}
		seen[s.Instance] = true
		if err := s.Redundancy.Validate(); err != nil {
			return fmt.Errorf("site roster: service %q: %w", s.Instance, err)
		}
	}
	return validateRedundancyGroups(r.Services)
}

func validateRedundancyGroups(services []RosterService) error {
	groups := make(map[string]RedundancyPolicy)
	for _, service := range services {
		policy := service.Redundancy
		if !policy.IsSingleActive() {
			continue
		}
		existing, found := groups[policy.Group]
		if found && !samePolicy(existing, policy) {
			return fmt.Errorf("site roster: single-active group %q has inconsistent policy", policy.Group)
		}
		groups[policy.Group] = policy
		if !rosterMember(policy.Members, service.Machine) {
			return fmt.Errorf("site roster: service %q is not in single-active group %q", service.Instance, policy.Group)
		}
	}
	for group, policy := range groups {
		for _, machine := range policy.Members {
			found := false
			for _, service := range services {
				if service.Machine == machine && service.Redundancy.IsSingleActive() && service.Redundancy.Group == group {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("site roster: single-active group %q member %q has no matching service", group, machine)
			}
		}
	}
	return nil
}

func rosterMember(members []string, machine string) bool {
	for _, member := range members {
		if member == machine {
			return true
		}
	}
	return false
}

func samePolicy(left, right RedundancyPolicy) bool {
	if left.Mode != right.Mode || left.Group != right.Group || left.Scope != right.Scope ||
		left.LeaseDuration != right.LeaseDuration || left.RetryInitial != right.RetryInitial ||
		left.RetryMax != right.RetryMax || left.FailoverTimeout != right.FailoverTimeout ||
		left.DrainTimeout != right.DrainTimeout || len(left.Members) != len(right.Members) {
		return false
	}
	for i := range left.Members {
		if left.Members[i] != right.Members[i] {
			return false
		}
	}
	return true
}

// SortedServices returns deterministic roster service order.
func (r SiteRoster) SortedServices() []RosterService {
	out := append([]RosterService(nil), r.Services...)
	sort.Slice(out, func(i, j int) bool { return out[i].Instance < out[j].Instance })
	return out
}
