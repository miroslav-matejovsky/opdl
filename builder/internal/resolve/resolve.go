package resolve

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

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
			d, err := descriptor(p, site, machine, platformName)
			if err != nil {
				return nil, fmt.Errorf("machine %q: %w", machine.Name, err)
			}
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
func descriptor(p *blueprint.Project, site blueprint.Site, machine blueprint.Machine, platformName string) (deployment.Descriptor, error) {
	inventory, err := siteServices(site)
	if err != nil {
		return deployment.Descriptor{}, err
	}
	return deployment.Descriptor{
		Platform:       platformName,
		Project:        p.Name,
		Environment:    p.Environment,
		Site:           site.Name,
		Machine:        machine.Name,
		MachineProfile: machine.MachineProfile,
		IP:             machine.IP,
		// The local half carries the probe policy the platform runs on this
		// machine; the site half carries what every machine of the site needs to
		// place a report it receives. They are resolved apart because only one of
		// them has an endpoint in it: a probe is aimed at this machine's own ip, so
		// no other machine has any use for these ports and paths.
		Services:     services(machine),
		SiteServices: inventory,
		// The machine's own store is resolved for every machine, standby or not:
		// a machine's facts are the machine's whether or not a second instance
		// exists to read them.
		MachineEventsFile: strings.TrimSpace(machine.EventstoreFile),
		Primary:           primaryInstance(site, machine),
		Standby:           instance(site, machine, deployment.RoleStandby),
		Lease:             lease(machine),
	}, nil
}

// services resolves the probe policy for every service this machine hosts, in
// authored order.
//
// The authored values are carried through unchanged. The blueprint already
// validated them, and a path in particular is kept byte for byte: a service that
// distinguishes "/Health" from "/health", or cares which characters are escaped,
// is probed at the path its author wrote.
func services(machine blueprint.Machine) []deployment.Service {
	resolved := make([]deployment.Service, 0, len(machine.Services))
	for _, authored := range machine.Services {
		resolved = append(resolved, deployment.Service{
			Name:        strings.TrimSpace(authored.Name),
			Role:        authored.Role,
			HealthCheck: healthCheck(authored.HealthCheck),
		})
	}
	return resolved
}

func healthCheck(authored blueprint.HealthCheck) deployment.HealthCheck {
	return deployment.HealthCheck{
		Type:     authored.Type,
		Port:     authored.Port,
		Path:     authored.Path,
		Interval: strings.TrimSpace(authored.Interval),
		Timeout:  strings.TrimSpace(authored.Timeout),
		Retries:  authored.Retries,
	}
}

// siteServices resolves the static inventory of every service unit at the site.
//
// Every machine of the site gets the same list, so it is built from the site
// rather than from the machine being resolved, and the order is the authored one
// — machines as the site lists them, services as each machine lists them. Two
// instances reducing the same reports against it produce the same view, and a
// rebuild that changed nothing produces the same descriptor.
//
// No endpoint crosses into it. What a remote instance needs is which units
// exist, who is expected to report on each, and how long a report stays fresh;
// what it must never have is a way to probe a service on someone else's machine.
func siteServices(site blueprint.Site) ([]deployment.SiteService, error) {
	var inventory []deployment.SiteService
	for _, machine := range site.Machines {
		roles := observerRoles(machine)
		for _, service := range machine.Services {
			fresh, err := freshFor(service.HealthCheck)
			if err != nil {
				return nil, fmt.Errorf("site %q machine %q service %q: derive health report freshness: %w",
					site.Name, machine.Name, service.Name, err)
			}
			inventory = append(inventory, deployment.SiteService{
				Machine:        strings.TrimSpace(machine.Name),
				MachineProfile: strings.TrimSpace(machine.MachineProfile),
				Service:        strings.TrimSpace(service.Name),
				ServiceRole:    service.Role,
				ObserverRoles:  append([]string(nil), roles...),
				FreshFor:       fresh,
			})
		}
	}
	return inventory, nil
}

// observerRoles lists the platform instances expected to report on a unit hosted
// by this machine.
//
// Both of a machine's instances probe every service on it, in every ownership
// state, so the answer is the machine's deployed instances and nothing about the
// service. A fresh list is returned per machine rather than shared, so no two
// inventory entries alias one slice.
func observerRoles(machine blueprint.Machine) []string {
	if machine.Endpoints(true) == nil {
		return []string{string(deployment.RolePrimary)}
	}
	return []string{string(deployment.RolePrimary), string(deployment.RoleStandby)}
}

// freshFor derives how long one report on a service stays current: two probe
// intervals plus one timeout.
//
// Two intervals is what makes a single lost report survivable, since the next is
// already due, and the timeout covers an attempt that took the longest it was
// allowed to before publishing. The blueprint validated both durations, so a
// parse failure here is impossible after blueprint validation, but it is still
// returned rather than hidden so resolution never turns a malformed policy into
// an unrelated descriptor error.
func freshFor(check blueprint.HealthCheck) (string, error) {
	interval, err := time.ParseDuration(strings.TrimSpace(check.Interval))
	if err != nil {
		return "", fmt.Errorf("parse interval %q: %w", check.Interval, err)
	}
	timeout, err := time.ParseDuration(strings.TrimSpace(check.Timeout))
	if err != nil {
		return "", fmt.Errorf("parse timeout %q: %w", check.Timeout, err)
	}
	if interval > (time.Duration(1<<63-1)-timeout)/2 {
		return "", fmt.Errorf("2 * interval %s + timeout %s overflows a duration", interval, timeout)
	}
	return (2*interval + timeout).String(), nil
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
