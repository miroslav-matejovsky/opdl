package resolve

import (
	"fmt"
	"slices"
	"strings"

	"github.com/miroslav-matejovsky/opdl/builder/deployment"
	"github.com/miroslav-matejovsky/opdl/builder/internal/blueprint"
)

// Plan is the full set of per-machine deployment descriptors derived from a
// project's blueprint.
type Plan struct {
	Project  string                  `json:"project"`
	Machines []deployment.Descriptor `json:"machines"`
}

// Build translates a validated blueprint into a Plan of deployment descriptors,
// one per machine. platformName identifies the product line the machines are
// built from; it is supplied by the builder, not the blueprint. Each descriptor
// is validated here, so the builder fails before compiling a machine that would
// not boot.
func Build(p *blueprint.Project, platformName string) (*Plan, error) {
	plan := &Plan{Project: p.Name}
	for _, site := range p.Sites {
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

// descriptor builds one machine's deployment descriptor from its place in the
// blueprint. This is where the layered topology collapses into a concrete,
// per-machine execution definition.
func descriptor(p *blueprint.Project, site blueprint.Site, machine blueprint.Machine, platformName string) deployment.Descriptor {
	return deployment.Descriptor{
		Platform:    platformName,
		Project:     p.Name,
		Environment: p.Environment,
		Site:        site.Name,
		Machine:     machine.Name,
		Role:        machine.Role,
		IP:          machine.IP,
		Services:    append([]string(nil), machine.Services...),
		Features: deployment.Features{
			Chaos:      p.Features.Chaos,
			Redundancy: p.Features.Redundancy,
		},
		EventFabric: eventFabric(site, machine),
	}
}

// eventFabric derives one machine's Event Fabric topology from its site. The
// fabric spans exactly one site, so the peers are that site's other machines and
// nothing else: a machine in another site, environment, or project is never a
// peer and forms its own fabric.
//
// Peers are ordered by machine name rather than by declaration, so the same
// topology always derives the same descriptor no matter how the blueprint was
// authored.
func eventFabric(site blueprint.Site, machine blueprint.Machine) deployment.EventFabric {
	peers := make([]deployment.EventFabricPeer, 0, len(site.Machines))
	for _, peer := range site.Machines {
		if peer.Name == machine.Name {
			continue
		}
		peers = append(peers, deployment.EventFabricPeer{
			Site:    site.Name,
			Machine: peer.Name,
			IP:      peer.IP,
		})
	}
	slices.SortFunc(peers, func(a, b deployment.EventFabricPeer) int {
		return strings.Compare(a.Machine, b.Machine)
	})
	return deployment.EventFabric{
		Peers: peers,
	}
}
