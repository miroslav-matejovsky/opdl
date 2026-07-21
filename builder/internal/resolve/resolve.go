package resolve

import (
	"encoding/hex"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/miroslav-matejovsky/opdl/builder/deployment"
	"github.com/miroslav-matejovsky/opdl/builder/internal/blueprint"
	"github.com/miroslav-matejovsky/opdl/utils/stablehash"
)

// Plan is the full set of per-machine deployment descriptors derived from a
// project's blueprint.
type Plan struct {
	Project  string                  `json:"project"`
	Machines []deployment.Descriptor `json:"machines"`
}

// Build translates a blueprint into a Plan of deployment descriptors, one per
// machine. platformName identifies the product line the machines are built from;
// it is supplied by the builder, not the blueprint.
//
// The blueprint is validated first, because resolution reads the platform policy
// every machine is required to state, and each resolved descriptor is validated
// after, so the builder fails before compiling a machine that would not boot.
func Build(p *blueprint.Project, platformName string) (*Plan, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
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
			Chaos: p.Features.Chaos,
		},
		Slots:       slots(machine),
		Fence:       fence(p, site, machine),
		EventFabric: eventFabric(site, machine),
	}
}

// fence derives a machine's ownership object name.
//
// The name is the authored namespace joined with a digest of the machine's whole
// identity. Hashing rather than concatenating the identity keeps the name inside
// the Windows kernel object name limit whatever a project, site, or machine is
// called, and keeps it free of characters a kernel namespace would reject.
//
// The digest covers project, environment, site, and machine, so no two machines
// derive the same object, and the same machine derives the same object on every
// build. It deliberately does not cover the machine's IP, role, or services: those
// can change without the machine becoming a different deployment identity, and an
// ownership object that moved when a machine was re-addressed would let an old
// process and a new one both be active.
func fence(p *blueprint.Project, site blueprint.Site, machine blueprint.Machine) deployment.Fence {
	digest := stablehash.Sum256(p.Name, p.Environment, site.Name, machine.Name)
	return deployment.Fence{
		Object: fmt.Sprintf("%s.fence.%s", machine.FenceNamespace(), hex.EncodeToString(digest[:16])),
	}
}

// slots resolves a machine's slot decision. The primary is never disabled: a
// machine with no primary process would deploy nothing that can serve. The
// standby decision is the blueprint's, copied through unchanged so the
// descriptor states it rather than implying it.
func slots(machine blueprint.Machine) deployment.Slots {
	return deployment.Slots{
		Primary: deployment.Slot{Disabled: false},
		Standby: deployment.Slot{Disabled: machine.Platform.Standby.Disabled},
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
		Nats:  eventFabricNats(site, machine),
		Peers: peers,
	}
}

// eventFabricNats derives a machine's one NATS topology from its site.
//
// Nothing here is authored. The machine's own addresses are its blueprint ports
// joined to its ip, and the route and server lists are consequences of which
// machines the site selects to store the journal. Letting a blueprint state
// those lists directly would let it split a site, drop a storage node, or point
// a machine at a journal that is not its own, and none of that would be visible
// in the descriptor as anything other than a working topology.
//
// Both lists are always non-nil, so an empty list serialises as [] and a
// descriptor reader can tell "no routes" from "not resolved".
func eventFabricNats(site blueprint.Site, machine blueprint.Machine) deployment.EventFabricNats {
	storage := siteStorageMachines(site)
	hostsStorage := slices.ContainsFunc(storage, func(m blueprint.Machine) bool {
		return m.Name == machine.Name
	})

	out := deployment.EventFabricNats{
		ClientAddress:  clientAddress(machine),
		ClusterAddress: clusterAddress(machine),
		Routes:         []string{},
		Servers:        []string{},
	}
	// A storage machine answers its own clients, so it connects to itself first
	// and falls back to its peers. That ordering is what keeps a promoted process
	// on its own server rather than routing its traffic through a peer.
	if hostsStorage {
		out.Servers = append(out.Servers, out.ClientAddress)
	}
	for _, peer := range storage {
		if peer.Name == machine.Name {
			continue
		}
		out.Servers = append(out.Servers, clientAddress(peer))
		// Only storage machines route, and only to each other. A site with one
		// storage machine has no peer to route to and binds no cluster listener.
		if hostsStorage {
			out.Routes = append(out.Routes, clusterAddress(peer))
		}
	}
	return out
}

// siteStorageMachines returns the machines of a site that store the site
// journal, sorted by name: one for a site smaller than three machines, the first
// three otherwise. Selecting by sorted name makes every machine of the site
// derive the same set without coordinating.
func siteStorageMachines(site blueprint.Site) []blueprint.Machine {
	sorted := slices.Clone(site.Machines)
	slices.SortFunc(sorted, func(a, b blueprint.Machine) int {
		return strings.Compare(a.Name, b.Name)
	})
	switch {
	case len(sorted) == 0:
		return nil
	case len(sorted) <= smallSiteMax:
		return sorted[:1]
	default:
		return sorted[:3]
	}
}

// smallSiteMax is the largest site that runs one storage machine. It matches the
// platform adapter's rule; both derive the same set from the same site.
const smallSiteMax = 2

// clientAddress is where machine's server serves the NATS client protocol.
func clientAddress(machine blueprint.Machine) string {
	return net.JoinHostPort(machine.IP, strconv.Itoa(machine.Platform.Nats.ClientPort))
}

// clusterAddress is where machine's server routes to the site's other storage
// machines.
func clusterAddress(machine blueprint.Machine) string {
	return net.JoinHostPort(machine.IP, strconv.Itoa(machine.Platform.Nats.ClusterPort))
}
