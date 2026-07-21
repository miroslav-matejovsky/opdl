package resolve

import (
	"fmt"
	"net"
	"slices"
	"strconv"
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
		Platform:       platformName,
		Project:        p.Name,
		Environment:    p.Environment,
		Site:           site.Name,
		Machine:        machine.Name,
		MachineProfile: machine.MachineProfile,
		IP:             machine.IP,
		Services:       append([]string(nil), machine.Services...),
		Features: deployment.Features{
			Chaos: p.Features.Chaos,
		},
		Instances: instances(site, machine),
		Lock:      lock(machine),
		Peers:     peers(site),
	}
}

// lock resolves a machine's local ownership lock when a Standby Instance is
// deployed. It returns nil when the machine deploys no standby and has no lock.
func lock(machine blueprint.Machine) *deployment.Lock {
	authored := machine.Lock()
	if authored == nil {
		return nil
	}
	return &deployment.Lock{
		WindowsMutex: authored.WindowsMutex,
	}
}

// instances resolves a machine's two platform instances. The primary is never
// disabled: a machine with no Primary Instance would deploy nothing that can
// serve. The standby decision is the blueprint's, copied through unchanged so the
// descriptor states it rather than implying it.
func instances(site blueprint.Site, machine blueprint.Machine) deployment.Instances {
	return deployment.Instances{
		Primary: instance(site, machine, deployment.RolePrimary),
		Standby: instance(site, machine, deployment.RoleStandby),
	}
}

// instance resolves one platform instance: what runs it and every endpoint it
// binds. An instance that is not deployed resolves to the disabled record and
// nothing else, because an endpoint no process will bind would read exactly like
// one that will.
func instance(site blueprint.Site, machine blueprint.Machine, role deployment.PlatformInstanceRole) deployment.Instance {
	endpoints := machine.Endpoints(role == deployment.RoleStandby)
	if endpoints == nil {
		return deployment.Instance{Disabled: true}
	}
	nats := instanceNats(site, machine, role)
	return deployment.Instance{
		Disabled:   false,
		Service:    winService(machine, role == deployment.RoleStandby),
		APIAddress: address(machine.IP, endpoints.APIPort),
		Nats:       &nats,
	}
}

// winService resolves one instance's Windows Service identity, or nil when that
// instance is not deployed. The blueprint authors the name; the builder fills the
// display name default so the descriptor states a complete identity rather than
// leaving a consumer to guess one.
func winService(machine blueprint.Machine, standby bool) *deployment.WinService {
	authored := machine.WinServiceIdentity(standby)
	if authored == nil {
		return nil
	}
	return &deployment.WinService{
		Name:        authored.Name,
		DisplayName: authored.DisplayName,
		Description: authored.Description,
	}
}

// peers derives a site's membership: every platform instance every machine of the
// site deploys.
//
// The members are instances, not machines. A machine that deploys only a Primary
// Instance contributes one peer; a machine that deploys a Standby Instance as
// well contributes two, and those two are separate members with separate
// endpoints rather than one member with a spare.
//
// The list is the same for every machine of the site, including this machine's
// own instances. One descriptor is read by both instances of a machine, so a list
// that left out its reader could not be written once for two readers; each
// running instance recognises itself by machine and role.
//
// Ordering is by machine name, then Primary before Standby, so every machine of a
// site derives the same list however the blueprint was authored.
func peers(site blueprint.Site) []deployment.Peer {
	machines := slices.Clone(site.Machines)
	slices.SortFunc(machines, func(a, b blueprint.Machine) int {
		return strings.Compare(a.Name, b.Name)
	})
	peers := make([]deployment.Peer, 0, len(machines)*2)
	for _, machine := range machines {
		for _, role := range []deployment.PlatformInstanceRole{deployment.RolePrimary, deployment.RoleStandby} {
			endpoints := machine.Endpoints(role == deployment.RoleStandby)
			if endpoints == nil {
				continue
			}
			peers = append(peers, deployment.Peer{
				Site:       site.Name,
				Machine:    machine.Name,
				Role:       role,
				IP:         machine.IP,
				APIAddress: address(machine.IP, endpoints.APIPort),
				Nats: deployment.PeerNats{
					ClientAddress:  address(machine.IP, endpoints.ClientPort),
					ClusterAddress: address(machine.IP, endpoints.ClusterPort),
				},
			})
		}
	}
	return peers
}

// instanceNats derives one instance's NATS topology from its site.
//
// Nothing here is authored. The instance's own addresses are its blueprint ports
// joined to its machine's ip, and the route and server lists are consequences of
// which machines the site selects to store the journal. Letting a blueprint state
// those lists directly would let it split a site, drop a storage server, or point
// an instance at a journal that is not its own, and none of that would be visible
// in the descriptor as anything other than a working topology.
//
// Storage is selected by machine and served by instance: every instance a storage
// machine deploys runs a server, and a machine's two servers route to each other
// like any other pair. That is why a one-machine site with a standby still has a
// cluster.
//
// Both lists are always non-nil, so an empty list serialises as [] and a
// descriptor reader can tell "no routes" from "not resolved".
func instanceNats(site blueprint.Site, machine blueprint.Machine, role deployment.PlatformInstanceRole) deployment.Nats {
	endpoints := machine.Endpoints(role == deployment.RoleStandby)
	out := deployment.Nats{
		ClientAddress:  address(machine.IP, endpoints.ClientPort),
		ClusterAddress: address(machine.IP, endpoints.ClusterPort),
		Routes:         []string{},
		Servers:        []string{},
	}
	storage := siteStorageServers(site)
	hostsStorage := slices.ContainsFunc(storage, func(s deployment.Peer) bool {
		return s.Machine == machine.Name
	})
	// A storage server answers its own clients, so it connects to itself first and
	// falls back to the rest. That ordering is what keeps an Active instance on its
	// own server rather than routing its traffic through a peer.
	if hostsStorage {
		out.Servers = append(out.Servers, out.ClientAddress)
	}
	for _, server := range storage {
		if server.Machine == machine.Name && server.Role == role {
			continue
		}
		out.Servers = append(out.Servers, server.Nats.ClientAddress)
		// Only instances on storage machines route, and only to each other. A site
		// with one storage server has no peer to route to and binds no cluster
		// listener.
		if hostsStorage {
			out.Routes = append(out.Routes, server.Nats.ClusterAddress)
		}
	}
	return out
}

// siteStorageServers returns the platform instances that serve the site journal,
// in peer order: the instances of the machines siteStorageMachines selects.
func siteStorageServers(site blueprint.Site) []deployment.Peer {
	storage := siteStorageMachines(site)
	servers := make([]deployment.Peer, 0, len(storage)*2)
	for _, peer := range peers(site) {
		if slices.ContainsFunc(storage, func(m blueprint.Machine) bool { return m.Name == peer.Machine }) {
			servers = append(servers, peer)
		}
	}
	return servers
}

// siteStorageMachines returns the machines of a site that store the site
// journal, sorted by name: one for a site smaller than three machines, the first
// three otherwise. Selecting by sorted name makes every machine of the site
// derive the same set without coordinating.
//
// The unit is the machine because that is the failure domain. A machine stores
// one copy of the journal however many instances it deploys.
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

// address joins a machine's ip with one of its instances' authored ports.
func address(ip string, port int) string {
	return net.JoinHostPort(ip, strconv.Itoa(port))
}
