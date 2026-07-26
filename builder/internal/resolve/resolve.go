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
		Instances:      instances(machine),
		Lease:          lease(machine),
		Peers:          peers(site),
	}
}

// lease resolves a machine's local Primary Ownership lease when a Standby
// Instance is deployed. It returns nil when the machine deploys no standby and
// has no lease.
func lease(machine blueprint.Machine) *deployment.Lease {
	authored := machine.Lease()
	if authored == nil {
		return nil
	}
	return &deployment.Lease{
		File:                  authored.File,
		Duration:              authored.Duration,
		RenewalInterval:       authored.RenewalInterval,
		HealthCheckInterval:   authored.HealthCheckInterval,
		FailbackStabilization: authored.FailbackStabilization,
	}
}

// instances resolves a machine's two platform instances. The primary is never
// disabled: a machine with no Primary Instance would deploy nothing that can
// serve. The standby decision is the blueprint's, copied through unchanged so the
// descriptor states it rather than implying it.
func instances(machine blueprint.Machine) deployment.Instances {
	return deployment.Instances{
		Primary: instance(machine, deployment.RolePrimary),
		Standby: instance(machine, deployment.RoleStandby),
	}
}

// instance resolves one platform instance: what runs it and every endpoint it
// binds. An instance that is not deployed resolves to the disabled record and
// nothing else, because an endpoint no process will bind would read exactly like
// one that will.
func instance(machine blueprint.Machine, role deployment.PlatformInstanceRole) deployment.Instance {
	endpoints := machine.Endpoints(role == deployment.RoleStandby)
	if endpoints == nil {
		return deployment.Instance{Disabled: true}
	}
	return deployment.Instance{
		Disabled:   false,
		Service:    winService(machine, role == deployment.RoleStandby),
		DataDir:    machine.DataDir(role == deployment.RoleStandby),
		APIAddress: loopbackAddress(endpoints.APILocalPort),
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
			peer := deployment.Peer{
				Site:    site.Name,
				Machine: machine.Name,
				Role:    role,
				IP:      machine.IP,
			}
			peers = append(peers, peer)
		}
	}
	return peers
}

// loopbackAddress joins an instance's authored api local_port with 127.0.0.1.
//
// The platform API is machine-local: it answers for the instance running on that
// host, to an operator or a co-located service. It is never joined with the
// machine's ip, so no deployment can reach another machine's API and none of
// these ports is exposed to the network.
func loopbackAddress(port int) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}
