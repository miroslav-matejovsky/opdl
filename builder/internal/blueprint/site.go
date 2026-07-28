package blueprint

import (
	"fmt"
	"strings"
)

// Site is one location within a project holding a set of machines.
type Site struct {
	// Name is the site identifier, unique within its project.
	Name string `hcl:"name,label"`
	// NATS is the site's event fabric cluster policy. Required: every machine at
	// a site runs embedded servers that join one cluster, and the cluster has to
	// be named somewhere. It is named here because the site is what the cluster
	// spans; naming it per instance would let one machine of a site be authored
	// into a cluster the rest of the site is not in.
	NATS *SiteNATS `hcl:"nats,block"`
	// Machines are the deployable units placed at this site.
	Machines []Machine `hcl:"machine,block"`
}

// SiteNATS is a site's event fabric cluster policy.
//
// The site's cluster is the whole of the fabric: every deployed instance of
// every machine at the site runs an embedded server, and all of them join this
// one cluster, primaries and standbys alike. A machine's two instances are two
// members of it, not one.
type SiteNATS struct {
	// ClusterName is what the site's embedded servers call the cluster they
	// form. Servers route only to peers naming the same cluster, so it is what
	// keeps one site's fabric from merging with another's.
	ClusterName string `hcl:"cluster_name"`
}

// ClusterName returns the site's authored event fabric cluster name. It is
// empty only for a site that did not pass validation.
func (s Site) ClusterName() string {
	if s.NATS == nil {
		return ""
	}
	return strings.TrimSpace(s.NATS.ClusterName)
}

// validateSiteNATS checks a site states the cluster its machines' embedded
// servers join.
func validateSiteNATS(site Site) error {
	if site.NATS == nil {
		return fmt.Errorf("site %q: nats block is required; the site's machines form one event fabric cluster and it has to be named", site.Name)
	}
	name := site.NATS.ClusterName
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("site %q: nats.cluster_name is required", site.Name)
	}
	if name != strings.TrimSpace(name) {
		return fmt.Errorf("site %q: nats.cluster_name %q must not have leading or trailing whitespace", site.Name, name)
	}
	return nil
}
