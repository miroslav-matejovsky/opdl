package blueprint

import (
	"fmt"
	"net/url"
	"strings"
)

// Service is one service a machine hosts, and how the platform learns whether
// it is up.
//
// It is a block rather than a bare name because a service is not only something
// placed on a machine: it is something an operator expects to be watched. A name
// alone says where a service runs and nothing about what "running" means for it,
// which leaves every consumer to invent a probe of its own.
type Service struct {
	// Name is the service identifier, unique within its machine.
	Name string `hcl:"name,label"`
	// Role is the part this machine's copy of the service plays: "master" or
	// "slave". Required, because which copy leads is not something a reader
	// should have to infer from the machine it was placed on.
	Role string `hcl:"role"`
	// HealthCheck is how this service is probed. Required: a service the platform
	// cannot ask about is one nobody can be told has stopped, so hosting one is
	// not something a blueprint may leave unsaid.
	//
	// It is a value rather than a pointer so the decoder itself rejects a service
	// that states no probe, before any rule here runs.
	HealthCheck HealthCheck `hcl:"health_check,block"`
}

// The parts a machine's copy of a service plays.
const (
	// roleMaster is the copy that leads.
	roleMaster = "master"
	// roleSlave is the copy that follows.
	roleSlave = "slave"
)

// serviceRoles are the roles a blueprint may author, named in the error an
// unknown role produces so an author is told what is available.
var serviceRoles = []string{roleMaster, roleSlave}

// healthCheckHTTP probes a service by requesting a path over HTTP on the
// machine and reading the response status.
const healthCheckHTTP = "http"

// healthCheckTypes are the probe kinds a blueprint may author, named in the
// error an unknown type produces so an author is told what is available rather
// than only that their value was wrong.
var healthCheckTypes = []string{healthCheckHTTP}

// HealthCheck is how one service reports whether it is up: what to probe, how
// often, and how much failure to tolerate before the service is considered
// down.
//
// It is deployment policy, authored here rather than compiled into a service,
// because how hard to press a service and how long to wait for it are answers
// that differ per site and per machine, not per build.
//
// The durations are Go duration strings ("10s", "2s"), validated here and
// parsed by whoever runs the probe.
type HealthCheck struct {
	// Type is the kind of probe. "http" is the only kind today.
	Type string `hcl:"type"`
	// Port is the port on the machine the probe connects to. It is the service's
	// own listener, not the platform's.
	Port int `hcl:"port"`
	// Path is the HTTP path the probe requests, e.g. "/health". Required for the
	// "http" type. An optional query is allowed ("/health?verbose=1"); a fragment
	// or an absolute URL is not, because the host a probe connects to is the
	// machine's, resolved by the builder, and never authored here.
	//
	// The authored bytes are kept exactly as written. Validation reads the path
	// but never rewrites it, so a service that distinguishes "/Health" from
	// "/health", or cares which characters are escaped, is probed at the path its
	// author wrote.
	Path string `hcl:"path,optional"`
	// Interval is how often the probe runs.
	Interval string `hcl:"interval"`
	// Timeout bounds one probe attempt. It must be shorter than Interval, so a
	// slow probe cannot still be running when the next one is due.
	Timeout string `hcl:"timeout"`
	// Retries is how many consecutive failed probes mark the service down. It is
	// at least 1, because a service that tolerates no failure is still failed by
	// the first one.
	Retries int `hcl:"retries"`
}

// validateMachineServices checks the machine hosts at least one service, that
// each is named once, and that each states a known role and a complete health
// check.
func validateMachineServices(machine Machine) error {
	if len(machine.Services) == 0 {
		return fmt.Errorf("machine %q: at least one service is required", machine.Name)
	}
	assigned := make(map[string]bool, len(machine.Services))
	for _, service := range machine.Services {
		switch {
		case strings.TrimSpace(service.Name) == "":
			return fmt.Errorf("machine %q: service with empty name", machine.Name)
		case service.Name != strings.TrimSpace(service.Name):
			return fmt.Errorf("machine %q: service %q must not have leading or trailing whitespace", machine.Name, service.Name)
		case assigned[service.Name]:
			return fmt.Errorf("machine %q: service %q assigned more than once", machine.Name, service.Name)
		}
		assigned[service.Name] = true

		if err := validateServiceRole(machine.Name, service); err != nil {
			return err
		}
		if err := validateHealthCheck(machine.Name, service); err != nil {
			return err
		}
	}
	return nil
}

// validateServiceRole checks one service states a part it plays. The authored
// value is matched exactly rather than trimmed or folded, so a role reads the
// same everywhere it is written and a consumer can compare it literally.
func validateServiceRole(machineName string, service Service) error {
	switch role := service.Role; {
	case strings.TrimSpace(role) == "":
		return fmt.Errorf("machine %q: service %q: role is required", machineName, service.Name)
	case role == roleMaster, role == roleSlave:
		return nil
	default:
		return fmt.Errorf("machine %q: service %q: role %q is not a known role; the supported roles are %s",
			machineName, service.Name, service.Role, strings.Join(serviceRoles, ", "))
	}
}

// validateHealthCheck checks one service's authored probe.
func validateHealthCheck(machineName string, service Service) error {
	check := service.HealthCheck
	where := fmt.Sprintf("service %q: health_check", service.Name)

	// The authored type is matched exactly, like the role: a probe kind is a
	// fixed word, and " http " naming the same thing as "http" is a leniency the
	// next kind would have to repeat.
	switch {
	case strings.TrimSpace(check.Type) == "":
		return fmt.Errorf("machine %q: %s.type is required", machineName, where)
	case check.Type == healthCheckHTTP:
		if err := validateHTTPHealthCheck(machineName, where, check); err != nil {
			return err
		}
	default:
		return fmt.Errorf("machine %q: %s.type %q is not a known probe; the supported types are %s",
			machineName, where, check.Type, strings.Join(healthCheckTypes, ", "))
	}

	if err := validatePort(machineName, where+".port", check.Port); err != nil {
		return err
	}
	interval, err := validateDuration(machineName, where+".interval", check.Interval)
	if err != nil {
		return err
	}
	timeout, err := validateDuration(machineName, where+".timeout", check.Timeout)
	if err != nil {
		return err
	}
	if timeout >= interval {
		return fmt.Errorf("machine %q: %s.timeout %s must be shorter than interval %s",
			machineName, where, check.Timeout, check.Interval)
	}
	if check.Retries < 1 {
		return fmt.Errorf("machine %q: %s.retries must be at least 1, got %d", machineName, where, check.Retries)
	}
	return nil
}

// validateHTTPHealthCheck checks what the "http" probe kind requires beyond the
// fields every kind states.
//
// The path is authored as the request target and nothing more. The scheme is
// fixed by the probe type and the host is the machine's own ip, which the
// builder resolves; a path that carried either would be stating where to connect
// from the one field that describes what to ask for. So an absolute URL, a
// scheme, and a host are all rejected here rather than quietly ignored by
// whoever builds the request.
//
// A query is allowed, because a service may distinguish its health endpoints by
// one. A fragment is not: it is never sent to a server, so authoring one asks
// for something that cannot be probed.
func validateHTTPHealthCheck(machineName, where string, check HealthCheck) error {
	path := check.Path
	switch {
	case strings.TrimSpace(path) == "":
		return fmt.Errorf("machine %q: %s.path is required for an %s probe", machineName, where, healthCheckHTTP)
	case path != strings.TrimSpace(path):
		return fmt.Errorf("machine %q: %s.path %q must not have leading or trailing whitespace", machineName, where, path)
	}
	// Checked before parsing, because url.Parse accepts most control characters
	// and they would reach the request as a header injection or as a target no
	// server can route.
	if strings.IndexFunc(path, isControl) >= 0 {
		return fmt.Errorf("machine %q: %s.path %q must not contain control characters", machineName, where, path)
	}
	// Matched on the authored bytes rather than on the parsed value, so a bare
	// trailing "#" is rejected too: it parses to an empty fragment, which would
	// otherwise read as no fragment at all.
	if strings.Contains(path, "#") {
		return fmt.Errorf("machine %q: %s.path %q must not contain a fragment; a fragment is never sent to a server", machineName, where, path)
	}
	parsed, err := url.Parse(path)
	if err != nil {
		return fmt.Errorf("machine %q: %s.path %q is not a valid request path: %w", machineName, where, path, err)
	}
	// Where the probe connects is checked before how the path is shaped, so an
	// author who wrote a whole URL is told that rather than that their path is
	// missing a leading slash. "//host/health" is the case the two checks split:
	// it has no scheme and does start with a slash, and it still names a host.
	switch {
	case parsed.Scheme != "":
		return fmt.Errorf("machine %q: %s.path %q must not be an absolute URL; the probe connects to the machine's own ip on %s.port",
			machineName, where, path, where)
	case parsed.Host != "":
		return fmt.Errorf("machine %q: %s.path %q must not name a host; the probe connects to the machine's own ip on %s.port",
			machineName, where, path, where)
	case !strings.HasPrefix(path, "/"):
		return fmt.Errorf("machine %q: %s.path %q must start with %q", machineName, where, path, "/")
	}
	return nil
}

// isControl reports whether r is a character that must not appear in a request
// path: the C0 range and DEL.
func isControl(r rune) bool { return r < 0x20 || r == 0x7f }
