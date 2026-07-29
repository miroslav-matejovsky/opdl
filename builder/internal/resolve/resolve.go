package resolve

import (
	"fmt"
	"net"
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
		// The descriptor carries the names only. A machine's health checks are
		// deployment policy the platform does not yet run, so resolving them into
		// the runtime's contract would state a capability that does not exist.
		Services: machine.ServiceNames(),
		// The machine's own store is resolved for every machine, standby or not:
		// a machine's facts are the machine's whether or not a second instance
		// exists to read them.
		MachineEventsFile: strings.TrimSpace(machine.EventstoreFile),
		Primary:           primaryInstance(site, machine),
		Standby:           instance(site, machine, deployment.RoleStandby),
		Lease:             lease(machine),
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

// instance resolves one platform instance: what runs it and every endpoint it
// binds. It returns nil when the machine deploys no instance in that role, which
// is how a standby-less machine resolves: an endpoint no process will bind would
// read exactly like one that will, so it is absent rather than blank.
//
// The site is here for the instance's embedded event fabric server, which is the
// one thing on an instance record resolved from above the machine. The site
// names the cluster its servers form, and it holds the peers this one routes to,
// which are on other machines and cannot be read off this one.
//
// The primary is always deployed. A blueprint that somehow authored none resolves
// to a nil record here and is rejected by Descriptor.Validate rather than
// silently producing a machine that serves nothing.
func instance(site blueprint.Site, machine blueprint.Machine, role deployment.PlatformInstanceRole) *deployment.Instance {
	standby := role == deployment.RoleStandby
	endpoints := machine.Endpoints(standby)
	if endpoints == nil {
		return nil
	}
	files := machine.Files(standby)
	return &deployment.Instance{
		Service:              winService(machine, standby),
		EventsFile:           files.EventlogFile,
		StateFile:            files.StateFile,
		LogFile:              files.LogFile,
		APIAddress:           loopbackAddress(endpoints.APILocalPort),
		APIReadHeaderTimeout: endpoints.APIReadHeaderTimeout,
		APIShutdownTimeout:   endpoints.APIShutdownTimeout,
		NATS: deployment.NATS{
			ServerName:     natsServerName(machine, role),
			ClusterName:    site.ClusterName(),
			ClusterAddress: machineAddress(machine.IP, endpoints.NATSClusterPort),
			Routes:         natsRoutes(site, machine, role),
		},
	}
}

// natsRoutes lists the peers this instance's embedded server dials to join its
// site's cluster: every other deployed instance at the site, its own machine's
// other instance included.
//
// The whole site is one cluster, so the list spans machines. It is resolved per
// instance and leaves that instance out, because a server that routed to itself
// would be dialing its own listener; the peers are the other members.
//
// Routes are mutual: every member carries every other member. NATS opens one
// route per pair and drops the duplicate, so the full list on each side is what
// makes the cluster form no matter which member starts first.
func natsRoutes(site blueprint.Site, self blueprint.Machine, role deployment.PlatformInstanceRole) []string {
	var routes []string
	for _, peer := range site.Machines {
		for _, peerRole := range []deployment.PlatformInstanceRole{deployment.RolePrimary, deployment.RoleStandby} {
			if peer.Name == self.Name && peerRole == role {
				continue
			}
			endpoints := peer.Endpoints(peerRole == deployment.RoleStandby)
			if endpoints == nil {
				continue
			}
			routes = append(routes, natsRouteURL(peer.IP, endpoints.NATSClusterPort))
		}
	}
	return routes
}

// natsRouteURL is how one peer's cluster address is written for the server that
// dials it. NATS takes routes as URLs rather than addresses, and the scheme is
// what says the connection is a route rather than a client.
func natsRouteURL(ip string, port int) string {
	return "nats://" + net.JoinHostPort(ip, strconv.Itoa(port))
}

// natsServerName names one instance's embedded event fabric server.
//
// The blueprint authors the port and the builder resolves the identity, exactly
// as it does for the API address. Machine names are unique within a project and
// an instance's role is fixed at build time, so "<machine>-<role>" names one
// server in the whole project and stays the same across rebuilds of it.
func natsServerName(machine blueprint.Machine, role deployment.PlatformInstanceRole) string {
	return machine.Name + "-" + string(role)
}

// primaryInstance resolves the machine's mandatory Primary Instance record.
func primaryInstance(site blueprint.Site, machine blueprint.Machine) deployment.Instance {
	resolved := instance(site, machine, deployment.RolePrimary)
	if resolved == nil {
		return deployment.Instance{}
	}
	return *resolved
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

// loopbackAddress joins an instance's authored api local_port with 127.0.0.1.
//
// The platform API is machine-local: it answers for the instance running on that
// host, to an operator or a co-located service. It is never joined with the
// machine's ip, so no deployment can reach another machine's API and none of
// these ports is exposed to the network.
func loopbackAddress(port int) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

// machineAddress joins a port with the machine's own ip.
//
// It is the opposite decision from loopbackAddress, and the event fabric's
// cluster address is what it is for. The site's servers form one cluster across
// machines, so a peer on another host has to be able to reach this one. An
// address on loopback would be reachable only from the machine that binds it,
// which is a cluster that can never have more than one member.
func machineAddress(ip string, port int) string {
	return net.JoinHostPort(strings.TrimSpace(ip), strconv.Itoa(port))
}
