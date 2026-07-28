package redundancy

import "fmt"

// InstanceRole identifies which of a machine's two fixed instances a process is.
//
// The roles are fixed and intentional. A machine deploys a Primary Instance and,
// when its blueprint enables one, a Standby Instance. The role is decided at
// build time, carried in the deployment package, and named in the Windows Service
// that runs the process. It is not assigned at runtime, negotiated, or exchanged.
//
// A role is not a state. A Standby Instance that takes over does not become the
// Primary Instance: it operates in the Active state until ownership returns. See
// State for the other axis.
type InstanceRole string

const (
	// RolePrimary is the Primary Instance, the preferred owner under the Preferred
	// Primary policy.
	RolePrimary InstanceRole = "primary"
	// RoleStandby is the Standby Instance, deployed only when the machine's
	// blueprint enables it.
	RoleStandby InstanceRole = "standby"
)

// ParseRole parses an instance role. The two values are an operator-visible
// contract: they are the -instance launch argument, the Windows Service that
// runs the process, and the process role in every event's origin.
func ParseRole(s string) (InstanceRole, error) {
	switch InstanceRole(s) {
	case RolePrimary, RoleStandby:
		return InstanceRole(s), nil
	default:
		return "", fmt.Errorf("redundancy: invalid instance role %q: want %q or %q", s, RolePrimary, RoleStandby)
	}
}

// String returns the instance role.
func (r InstanceRole) String() string { return string(r) }

// Valid reports whether r is a defined instance role.
func (r InstanceRole) Valid() bool { return r == RolePrimary || r == RoleStandby }

// OperationalName returns "<machine>/<role>" for logs and diagnostics.
//
// It is never a domain identity. Domain and durable-handler identities stay
// machine-scoped, so a machine's two instances never become two independent
// voters in whatever domain protocol runs above them.
func OperationalName(machine string, role InstanceRole) string {
	return machine + "/" + string(role)
}
