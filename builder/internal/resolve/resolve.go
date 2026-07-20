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
			Chaos: p.Features.Chaos,
		},
		Slots:       slots(site, machine),
		EventFabric: eventFabric(site, machine),
	}
}

// slots resolves a machine's slot topology. Primary is always present,
// and standby is enabled only when an explicit standby subsection is present.
func slots(site blueprint.Site, machine blueprint.Machine) deployment.Slots {
	out := deployment.Slots{
		Primary: deployment.Slot{
			EventFabric: deployment.SlotEventFabric{
				Nats: eventFabricNats(site, machine, true),
			},
		},
	}
	if machine.Platform != nil && machine.Platform.Standby != nil {
		out.Standby = &deployment.Slot{
			EventFabric: deployment.SlotEventFabric{
				Nats: eventFabricNats(site, machine, false),
			},
		}
	}
	return out
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

// eventFabricNats derives one slot's Event Fabric NATS configuration from the
// blueprint. The explicit client, cluster, and monitor addresses from the
// machine's platform.nats (or platform.standby.nats) block are carried directly into the descriptor.
// If servers or routes are omitted from the blueprint, they are derived from the
// site's storage nodes (up to the first three machines in the site sorted by name).
func machineSlotNats(machine blueprint.Machine, primary bool) blueprint.Nats {
	if machine.Platform == nil {
		return blueprint.Nats{}
	}
	if primary && machine.Platform.Nats != nil {
		return *machine.Platform.Nats
	}
	if !primary && machine.Platform.Standby != nil && machine.Platform.Standby.Nats != nil {
		return *machine.Platform.Standby.Nats
	}
	return blueprint.Nats{}
}

func peerSlotNats(machine blueprint.Machine, primary bool) blueprint.Nats {
	if machine.Platform == nil {
		return blueprint.Nats{}
	}
	if !primary && machine.Platform.Standby != nil && machine.Platform.Standby.Nats != nil {
		return *machine.Platform.Standby.Nats
	}
	if machine.Platform.Nats != nil {
		return *machine.Platform.Nats
	}
	return blueprint.Nats{}
}

func siteStorageMachines(site blueprint.Site) []blueprint.Machine {
	sorted := append([]blueprint.Machine(nil), site.Machines...)
	slices.SortFunc(sorted, func(a, b blueprint.Machine) int {
		return strings.Compare(a.Name, b.Name)
	})
	if len(sorted) <= 2 {
		if len(sorted) > 0 {
			return sorted[:1]
		}
		return nil
	}
	return sorted[:3]
}

func deriveServers(storageMachines []blueprint.Machine, clientAddr string, hostsStorage, primary bool) []string {
	var servers []string
	if hostsStorage && clientAddr != "" {
		servers = append(servers, clientAddr)
	}
	for _, sm := range storageMachines {
		smNats := peerSlotNats(sm, primary)
		if smNats.ClientAddress != "" && smNats.ClientAddress != clientAddr {
			servers = append(servers, smNats.ClientAddress)
		}
	}
	return servers
}

func deriveRoutes(storageMachines []blueprint.Machine, machineName string, hostsStorage, primary bool) []string {
	if !hostsStorage {
		return nil
	}
	var routes []string
	for _, sm := range storageMachines {
		if sm.Name == machineName {
			continue
		}
		smNats := peerSlotNats(sm, primary)
		if smNats.ClusterAddress != "" {
			routes = append(routes, smNats.ClusterAddress)
		}
	}
	return routes
}

func eventFabricNats(site blueprint.Site, machine blueprint.Machine, primary bool) deployment.EventFabricNats {
	nats := machineSlotNats(machine, primary)
	out := deployment.EventFabricNats{
		ClientAddress:  nats.ClientAddress,
		ClusterAddress: nats.ClusterAddress,
		MonitorAddress: nats.MonitorAddress,
	}

	if nats.Routes != nil {
		if len(nats.Routes) == 0 {
			out.Routes = []string{}
		} else {
			out.Routes = append([]string(nil), nats.Routes...)
		}
	}
	if nats.Servers != nil {
		if len(nats.Servers) == 0 {
			out.Servers = []string{}
		} else {
			out.Servers = append([]string(nil), nats.Servers...)
		}
	}
	if nats.Routes != nil && nats.Servers != nil {
		return out
	}

	storageMachines := siteStorageMachines(site)
	hostsStorage := false
	for _, sm := range storageMachines {
		if sm.Name == machine.Name {
			hostsStorage = true
			break
		}
	}

	if nats.Servers == nil {
		out.Servers = deriveServers(storageMachines, nats.ClientAddress, hostsStorage, primary)
	}
	if nats.Routes == nil {
		out.Routes = deriveRoutes(storageMachines, machine.Name, hostsStorage, primary)
	}
	return out
}
