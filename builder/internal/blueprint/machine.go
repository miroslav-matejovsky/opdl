package blueprint

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"
)

// Machine is one deployable unit within a site.
type Machine struct {
	// Name is the machine identifier, unique within its project.
	Name string `hcl:"name,label"`
	// MachineProfile is the machine's purpose, e.g. "sensor-node".
	MachineProfile string `hcl:"profile"`
	// IP is the machine's network address, e.g. "10.0.1.10".
	IP string `hcl:"ip"`
	// Services are the services this machine hosts, each with the health check
	// that says whether it is up.
	Services []Service `hcl:"service,block"`
	// EventstoreFile is the machine's own append-only event store: the shared file
	// both instances append machine-scoped events to. Required on every machine.
	EventstoreFile string `hcl:"eventstore_file,optional"`
	// Primary is the machine's Primary Instance policy. Required on every machine.
	Primary *Primary `hcl:"primary,block"`
	// Standby is the machine's local redundancy policy, and where a deployed
	// Standby Instance states its own files, lease, api, and winservice.
	Standby *Standby `hcl:"standby,block"`
}

// ServiceNames lists the machine's services by name, in authored order, for
// consumers that place a service rather than probe it.
func (m Machine) ServiceNames() []string {
	names := make([]string, 0, len(m.Services))
	for _, service := range m.Services {
		names = append(names, strings.TrimSpace(service.Name))
	}
	return names
}

// Lease returns the machine's authored ownership lease policy, or nil when the
// blueprint does not deploy a standby or states no lease.
func (m Machine) Lease() *Lease {
	if m.Standby == nil || m.Standby.Disabled {
		return nil
	}
	return m.Standby.Lease
}

// Endpoints resolves one instance's authored endpoint ports, or nil when that
// instance is not deployed.
func (m Machine) Endpoints(standby bool) *Endpoints {
	var api *API
	if !standby {
		if m.Primary == nil {
			return nil
		}
		api = m.Primary.API
	} else {
		if m.Standby == nil || m.Standby.Disabled {
			return nil
		}
		api = m.Standby.API
	}
	if api == nil {
		return nil
	}
	return &Endpoints{
		APILocalPort:         api.LocalPort,
		APIReadHeaderTimeout: strings.TrimSpace(api.ReadHeaderTimeout),
		APIShutdownTimeout:   strings.TrimSpace(api.ShutdownTimeout),
	}
}

// Files returns one instance's authored local files, or the zero InstanceFiles
// when that instance is not deployed.
func (m Machine) Files(standby bool) InstanceFiles {
	if !standby {
		if m.Primary == nil {
			return InstanceFiles{}
		}
		return InstanceFiles{
			EventlogFile: strings.TrimSpace(m.Primary.EventlogFile),
			StateFile:    strings.TrimSpace(m.Primary.StateFile),
			LogFile:      strings.TrimSpace(m.Primary.LogFile),
		}
	}
	if m.Standby == nil || m.Standby.Disabled {
		return InstanceFiles{}
	}
	return InstanceFiles{
		EventlogFile: strings.TrimSpace(m.Standby.EventlogFile),
		StateFile:    strings.TrimSpace(m.Standby.StateFile),
		LogFile:      strings.TrimSpace(m.Standby.LogFile),
	}
}

// WinServiceIdentity resolves one instance's service identity, filling the
// display name default. It returns nil when the instance is not deployed.
func (m Machine) WinServiceIdentity(standby bool) *WinService {
	var authored *WinService
	if !standby {
		if m.Primary == nil {
			return nil
		}
		authored = m.Primary.WinService
	} else {
		if m.Standby == nil || m.Standby.Disabled {
			return nil
		}
		authored = m.Standby.WinService
	}
	if authored == nil {
		return nil
	}
	resolved := *authored
	if strings.TrimSpace(resolved.DisplayName) == "" {
		resolved.DisplayName = resolved.Name
	}
	return &resolved
}

// validateMachine checks one machine's identity, services, and instance policy.
func (p *Project) validateMachine(site Site, machine Machine, machineNames map[string]bool) error {
	if strings.TrimSpace(machine.Name) == "" {
		return fmt.Errorf("site %q: machine with empty name", site.Name)
	}
	if machineNames[machine.Name] {
		return fmt.Errorf("project %q: duplicate machine %q", p.Name, machine.Name)
	}
	machineNames[machine.Name] = true

	if strings.TrimSpace(machine.MachineProfile) == "" {
		return fmt.Errorf("machine %q: profile is required", machine.Name)
	}
	if strings.TrimSpace(machine.IP) == "" {
		return fmt.Errorf("machine %q: ip is required", machine.Name)
	}
	if net.ParseIP(machine.IP) == nil {
		return fmt.Errorf("machine %q: ip %q is not a valid IP address", machine.Name, machine.IP)
	}
	if err := validateMachineServices(machine); err != nil {
		return err
	}

	if machine.Primary == nil {
		return fmt.Errorf("machine %q: primary block is required", machine.Name)
	}
	if machine.Standby == nil {
		return fmt.Errorf("machine %q: standby block is required", machine.Name)
	}
	if err := validatePrimaryEndpoints(machine); err != nil {
		return err
	}
	if err := validateStandbyEndpoints(machine); err != nil {
		return err
	}
	if err := validateInstanceFiles(machine); err != nil {
		return err
	}
	if err := validateMachineFiles(machine); err != nil {
		return err
	}
	if err := validateMachinePorts(machine); err != nil {
		return err
	}
	if err := validateWinServices(machine); err != nil {
		return err
	}
	return validateLease(machine)
}

func validateMachinePorts(machine Machine) error {
	type listener struct {
		where string
		port  int
	}
	listeners := []listener{
		{"primary.api.local_port", machine.Primary.API.LocalPort},
	}
	if !machine.Standby.Disabled {
		listeners = append(listeners,
			listener{"standby.api.local_port", machine.Standby.API.LocalPort},
		)
	}
	// A service's health check port is a listener on this machine like any other.
	// The platform's own APIs are on loopback and a service's endpoint usually is
	// not, but both are bound on one host, so a port authored twice is still a
	// machine where the second listener cannot come up.
	for _, service := range machine.Services {
		listeners = append(listeners, listener{
			fmt.Sprintf("service %q health_check.port", service.Name),
			service.HealthCheck.Port,
		})
	}
	taken := make(map[int]string, len(listeners))
	for _, l := range listeners {
		if owner, used := taken[l.port]; used {
			return fmt.Errorf("machine %q: %s and %s are both %d; every listener on a machine needs its own port", machine.Name, owner, l.where, l.port)
		}
		taken[l.port] = l.where
	}
	return nil
}

// validateMachineFiles checks the machine states its own shared event store,
// and that no instance was authored onto it.
func validateMachineFiles(machine Machine) error {
	store := strings.TrimSpace(machine.EventstoreFile)
	if store == "" {
		return fmt.Errorf("machine %q: eventstore_file is required", machine.Name)
	}
	if machine.EventstoreFile != store {
		return fmt.Errorf("machine %q: eventstore_file %q must not have leading or trailing whitespace", machine.Name, machine.EventstoreFile)
	}

	others := authoredFiles(machine)
	if standby := machine.Standby; standby != nil && !standby.Disabled && standby.Lease != nil {
		others = append(others, instanceFile{"standby.lease.file", standby.Lease.File})
	}
	for _, other := range others {
		if samePath(other.path, store) {
			return fmt.Errorf("machine %q: eventstore_file and %s are both %q; the machine's shared store is not one of its instances' files",
				machine.Name, other.where, machine.EventstoreFile)
		}
	}
	return nil
}

// samePath reports whether two authored paths name the same file on Windows.
func samePath(a, b string) bool { return pathKey(a) == pathKey(b) }

// pathKey normalizes an authored path for comparison: separators cleaned and
// case folded, because Windows paths are case-insensitive.
func pathKey(path string) string {
	return strings.ToLower(filepath.Clean(strings.TrimSpace(path)))
}
