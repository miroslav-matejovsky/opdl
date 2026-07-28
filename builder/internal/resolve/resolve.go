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
		Services:       append([]string(nil), machine.Services...),
		// The machine's own store is resolved for every machine, standby or not:
		// a machine's facts are the machine's whether or not a second instance
		// exists to read them.
		MachineEventsFile: strings.TrimSpace(machine.EventstoreFile),
		Primary:           primaryInstance(machine),
		Standby:           instance(machine, deployment.RoleStandby),
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
		LagBound:              authored.LagBound,
	}
}

// instance resolves one platform instance: what runs it and every endpoint it
// binds. It returns nil when the machine deploys no instance in that role, which
// is how a standby-less machine resolves: an endpoint no process will bind would
// read exactly like one that will, so it is absent rather than blank.
//
// The primary is always deployed. A blueprint that somehow authored none resolves
// to a nil record here and is rejected by Descriptor.Validate rather than
// silently producing a machine that serves nothing.
func instance(machine blueprint.Machine, role deployment.PlatformInstanceRole) *deployment.Instance {
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
		APIAddress:           loopbackAddress(endpoints.APILocalPort),
		APIReadHeaderTimeout: endpoints.APIReadHeaderTimeout,
		APIShutdownTimeout:   endpoints.APIShutdownTimeout,
	}
}

// primaryInstance resolves the machine's mandatory Primary Instance record.
func primaryInstance(machine blueprint.Machine) deployment.Instance {
	resolved := instance(machine, deployment.RolePrimary)
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
