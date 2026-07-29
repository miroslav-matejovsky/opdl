package blueprint

import (
	"fmt"
	"strings"
	"time"
)

// Primary is a machine's Primary Instance policy.
type Primary struct {
	// EventlogFile is the Primary Instance's append-only local event record.
	EventlogFile string `hcl:"eventlog_file,optional"`
	// StateFile is the Primary Instance's durable state record, which carries its
	// epoch counter.
	StateFile string `hcl:"state_file,optional"`
	// LogFile is the Primary Instance's structured application log. It is a
	// separate file from EventlogFile because the two answer different questions:
	// the event log records the facts the instance stated, and the application log
	// records what the process was doing while it stated them.
	LogFile string `hcl:"log_file,optional"`
	// API is the Primary Instance's local API endpoint policy.
	API *API `hcl:"api,block"`
	// NATS is the Primary Instance's embedded event fabric server policy.
	NATS *NATS `hcl:"nats,block"`
	// WinService is the Primary Instance's Windows Service identity.
	WinService *WinService `hcl:"winservice,block"`
}

// Standby is a machine's local redundancy policy, and the Standby Instance's own
// policy when one is deployed.
type Standby struct {
	// Disabled opts the machine out of a second local process. It is required, so
	// omitting the attribute cannot silently enable or disable redundancy.
	Disabled bool `hcl:"disabled"`
	// EventlogFile is the Standby Instance's append-only local event record. It is
	// required when the Standby Instance is deployed and rejected when it is not.
	EventlogFile string `hcl:"eventlog_file,optional"`
	// StateFile is the Standby Instance's durable state record, which carries its
	// epoch counter. It is required when the Standby Instance is deployed and
	// rejected when it is not.
	StateFile string `hcl:"state_file,optional"`
	// LogFile is the Standby Instance's structured application log. It is required
	// when the Standby Instance is deployed and rejected when it is not.
	LogFile string `hcl:"log_file,optional"`
	// Lease is the machine's local Primary Ownership lease policy. It is required
	// when the Standby Instance is deployed and rejected when it is not.
	Lease *Lease `hcl:"lease,block"`
	// API is the Standby Instance's local API endpoint policy.
	API *API `hcl:"api,block"`
	// NATS is the Standby Instance's embedded event fabric server policy. It is
	// required when the Standby Instance is deployed and rejected when it is not.
	NATS *NATS `hcl:"nats,block"`
	// WinService is the Standby Instance's Windows Service identity.
	WinService *WinService `hcl:"winservice,block"`
}

// InstanceFiles are the files one instance owns on its machine's local disk.
type InstanceFiles struct {
	// EventlogFile is the append-only JSON Lines record the instance appends every
	// event it states to, including the ones about failing to start.
	EventlogFile string
	// StateFile is the durable record the instance carries across restarts and
	// crashes.
	StateFile string
	// LogFile is the append-only JSON Lines record the instance writes its
	// structured application logs to.
	LogFile string
}

// instanceFile is one authored path and the attribute it was authored on, so a
// validation failure names the attribute an author has to change.
type instanceFile struct{ where, path string }

// instanceFilesOf lists the files one instance owns, qualified by the block they
// were authored in. Every check that treats an instance's files as a set reads
// them from here, so a file added to InstanceFiles is validated everywhere at
// once rather than in whichever check remembered it.
func instanceFilesOf(block string, files InstanceFiles) []instanceFile {
	return []instanceFile{
		{block + ".eventlog_file", files.EventlogFile},
		{block + ".state_file", files.StateFile},
		{block + ".log_file", files.LogFile},
	}
}

// authoredFiles lists every local file the machine's deployed instances own.
func authoredFiles(machine Machine) []instanceFile {
	files := instanceFilesOf("primary", machine.Files(false))
	if standby := machine.Standby; standby != nil && !standby.Disabled {
		files = append(files, instanceFilesOf("standby", machine.Files(true))...)
	}
	return files
}

// Endpoints are one instance's authored listener ports and the timeouts that
// govern them.
type Endpoints struct {
	APILocalPort         int
	APIReadHeaderTimeout string
	APIShutdownTimeout   string
	// NATSClusterPort is the port the instance's embedded event fabric server
	// accepts route connections from its peers on.
	NATSClusterPort int
}

// API is one instance's local API endpoint policy.
type API struct {
	// LocalPort is the loopback port this instance serves its local API on.
	LocalPort int `hcl:"local_port"`
	// ReadHeaderTimeout bounds how long this instance's listener spends reading
	// an HTTP request's headers before it closes the connection.
	ReadHeaderTimeout string `hcl:"read_header_timeout"`
	// ShutdownTimeout bounds the graceful drain of this instance's listener when
	// it stops serving, whether it is stepping down or the process is leaving.
	ShutdownTimeout string `hcl:"shutdown_timeout"`
}

// NATS is one instance's embedded event fabric server policy.
//
// Every deployed instance runs its own embedded NATS server for its whole
// lifetime, exactly as it binds its own API listener, so the block is required
// on the primary and on a deployed standby and rejected on a standby that is
// not deployed.
//
// The server is at the instance level and the client that reaches it is at the
// site level; nothing here is shared between the two processes on a machine.
type NATS struct {
	// ClusterPort is the port this instance's embedded NATS server accepts
	// route connections from its peers on.
	//
	// It is the only port an instance's server binds. The platform's client
	// connects to its own server in process, and nothing outside the process is
	// a client of it, so there is no client port to author. What the cluster
	// port is for is the servers reaching each other: it is what lets the
	// embedded servers of a site form one NATS cluster.
	//
	// It is authored per instance because a machine's two instances run
	// together and each runs its own server, so each needs a port of its own.
	ClusterPort int `hcl:"cluster_port"`
}

// maxWinServiceName bounds a Windows Service name. The Service Control Manager
// limit is 256 characters.
const maxWinServiceName = 256

// WinService is one instance's Windows Service identity.
type WinService struct {
	// Name is the Windows Service name, as sc.exe and the Service Control Manager
	// use it. Required.
	Name string `hcl:"name"`
	// DisplayName is the name shown in the services list. It defaults to Name.
	DisplayName string `hcl:"display_name,optional"`
	// Description is the optional description shown in the services list.
	Description string `hcl:"description,optional"`
}

// Lease is a machine's local Primary Ownership lease policy.
type Lease struct {
	// File is the machine-wide lease file both instances read and write.
	File string `hcl:"file"`
	// Duration is how long a granted lease is valid without renewal.
	Duration string `hcl:"duration"`
	// RenewalInterval is how often the owner extends the lease.
	RenewalInterval string `hcl:"renewal_interval"`
	// HealthCheckInterval is how often a Passive instance evaluates promotion and
	// polls its peer's health.
	HealthCheckInterval string `hcl:"health_check_interval"`
	// FailbackStabilization is how long a returning Primary must be continuously
	// healthy before an Active Standby hands ownership back to it.
	FailbackStabilization string `hcl:"failback_stabilization"`
}

func validatePrimaryEndpoints(machine Machine) error {
	return validateInstanceEndpoints(machine, "primary", machine.Primary.API, machine.Primary.NATS)
}

func validateInstanceEndpoints(machine Machine, block string, api *API, nats *NATS) error {
	if api == nil {
		return fmt.Errorf("machine %q: %s.api block is required", machine.Name, block)
	}
	if err := validatePort(machine.Name, block+".api.local_port", api.LocalPort); err != nil {
		return err
	}
	if _, err := validateDuration(machine.Name, block+".api.read_header_timeout", api.ReadHeaderTimeout); err != nil {
		return err
	}
	if _, err := validateDuration(machine.Name, block+".api.shutdown_timeout", api.ShutdownTimeout); err != nil {
		return err
	}
	// The embedded event fabric server is checked with the API listener because
	// it is one: the instance binds it for its whole lifetime, and a machine
	// whose port it cannot take is a machine whose instance does not start.
	if nats == nil {
		return fmt.Errorf("machine %q: %s.nats block is required", machine.Name, block)
	}
	return validatePort(machine.Name, block+".nats.cluster_port", nats.ClusterPort)
}

// validateDuration parses one authored duration and requires it to be positive,
// naming the attribute an author has to change. It returns the parsed value for
// the checks that compare two of them.
func validateDuration(machineName, where, value string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return 0, fmt.Errorf("machine %q: %s is required", machineName, where)
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("machine %q: %s %q is not a valid duration: %w", machineName, where, value, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("machine %q: %s %s must be positive", machineName, where, d)
	}
	return d, nil
}

func validateStandbyEndpoints(machine Machine) error {
	standby := machine.Standby
	if !standby.Disabled {
		return validateInstanceEndpoints(machine, "standby", standby.API, standby.NATS)
	}
	if strings.TrimSpace(standby.EventlogFile) != "" {
		return fmt.Errorf("machine %q: standby.eventlog_file is set but the standby is disabled; remove it or deploy the standby", machine.Name)
	}
	if strings.TrimSpace(standby.StateFile) != "" {
		return fmt.Errorf("machine %q: standby.state_file is set but the standby is disabled; remove it or deploy the standby", machine.Name)
	}
	if strings.TrimSpace(standby.LogFile) != "" {
		return fmt.Errorf("machine %q: standby.log_file is set but the standby is disabled; remove it or deploy the standby", machine.Name)
	}
	if standby.Lease != nil {
		return fmt.Errorf("machine %q: standby.lease is set but the standby is disabled; remove it or deploy the standby", machine.Name)
	}
	if standby.API != nil {
		return fmt.Errorf("machine %q: standby.api is set but the standby is disabled; remove it or deploy the standby", machine.Name)
	}
	if standby.NATS != nil {
		return fmt.Errorf("machine %q: standby.nats is set but the standby is disabled; remove it or deploy the standby", machine.Name)
	}
	return nil
}

// validateInstanceFiles checks every deployed instance states each file it owns,
// and that no two of those files are the same path.
//
// The two instances of a machine run together, and each one writes all of its
// own files, so a path authored twice is two writers on one file whether the
// repeat is within an instance or across the pair.
func validateInstanceFiles(machine Machine) error {
	authored := authoredFiles(machine)
	for _, f := range authored {
		if f.path == "" {
			return fmt.Errorf("machine %q: %s is required", machine.Name, f.where)
		}
	}
	taken := make(map[string]string, len(authored))
	for _, f := range authored {
		key := pathKey(f.path)
		if owner, used := taken[key]; used {
			return fmt.Errorf("machine %q: %s and %s are both %q; every file an instance owns needs its own path",
				machine.Name, owner, f.where, f.path)
		}
		taken[key] = f.where
	}
	return nil
}

func validateWinServices(machine Machine) error {
	primary := machine.Primary.WinService
	if primary == nil {
		return fmt.Errorf("machine %q: primary.winservice block is required; every machine deploys a Primary Instance", machine.Name)
	}
	if err := validateWinService(machine.Name, "primary.winservice", primary); err != nil {
		return err
	}

	standby := machine.Standby.WinService
	if machine.Standby.Disabled {
		if standby != nil {
			return fmt.Errorf("machine %q: standby.winservice is set but the standby is disabled; remove it or deploy the standby", machine.Name)
		}
		return nil
	}
	if standby == nil {
		return fmt.Errorf("machine %q: standby.winservice block is required when the standby is deployed", machine.Name)
	}
	if err := validateWinService(machine.Name, "standby.winservice", standby); err != nil {
		return err
	}
	if primary.Name == standby.Name {
		return fmt.Errorf("machine %q: the Primary and Standby Instances both name their service %q; they run on one host and must differ", machine.Name, primary.Name)
	}
	return nil
}

func validateWinService(machineName, block string, service *WinService) error {
	name := service.Name
	switch {
	case strings.TrimSpace(name) == "":
		return fmt.Errorf("machine %q: %s.name is required", machineName, block)
	case name != strings.TrimSpace(name):
		return fmt.Errorf("machine %q: %s.name %q must not have leading or trailing whitespace", machineName, block, name)
	case len(name) > maxWinServiceName:
		return fmt.Errorf("machine %q: %s.name %q is longer than %d characters", machineName, block, name, maxWinServiceName)
	case strings.ContainsAny(name, `/\`):
		return fmt.Errorf("machine %q: %s.name %q must not contain a slash or backslash", machineName, block, name)
	}
	if len(service.DisplayName) > maxWinServiceName {
		return fmt.Errorf("machine %q: %s.display_name is longer than %d characters", machineName, block, maxWinServiceName)
	}
	return nil
}

func validateLease(machine Machine) error {
	standby := machine.Standby
	if standby.Disabled {
		return nil
	}
	if standby.Lease == nil {
		return fmt.Errorf("machine %q: standby.lease block is required when the standby is deployed", machine.Name)
	}
	lease := standby.Lease
	file := strings.TrimSpace(lease.File)
	if file == "" {
		return fmt.Errorf("machine %q: standby.lease.file is required", machine.Name)
	}
	if lease.File != file {
		return fmt.Errorf("machine %q: standby.lease.file %q must not have leading or trailing whitespace", machine.Name, lease.File)
	}
	duration, err := validateLeaseDuration(machine.Name, "duration", lease.Duration)
	if err != nil {
		return err
	}
	renewal, err := validateLeaseDuration(machine.Name, "renewal_interval", lease.RenewalInterval)
	if err != nil {
		return err
	}
	if _, err := validateLeaseDuration(machine.Name, "health_check_interval", lease.HealthCheckInterval); err != nil {
		return err
	}
	if _, err := validateLeaseDuration(machine.Name, "failback_stabilization", lease.FailbackStabilization); err != nil {
		return err
	}
	if renewal >= duration {
		return fmt.Errorf("machine %q: standby.lease.renewal_interval %s must be shorter than duration %s", machine.Name, lease.RenewalInterval, lease.Duration)
	}
	return nil
}

func validateLeaseDuration(machineName, attribute, value string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return 0, fmt.Errorf("machine %q: standby.lease.%s is required", machineName, attribute)
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("machine %q: standby.lease.%s %q is not a valid duration: %w", machineName, attribute, value, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("machine %q: standby.lease.%s %s must be positive", machineName, attribute, d)
	}
	return d, nil
}

func validatePort(machineName, where string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("machine %q: %s must be in range 1-65535, got %d", machineName, where, port)
	}
	return nil
}
