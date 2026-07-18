package redundancy

import "fmt"

// ProcessRole identifies the primary or standby process.
type ProcessRole string

const (
	// RolePrimary is the preferred process.
	RolePrimary ProcessRole = "primary"
	// RoleStandby is the optional failover process.
	RoleStandby ProcessRole = "standby"
)

// ParseRole parses a process role.
func ParseRole(s string) (ProcessRole, error) {
	switch ProcessRole(s) {
	case RolePrimary, RoleStandby:
		return ProcessRole(s), nil
	default:
		return "", fmt.Errorf("redundancy: invalid process role %q: want %q or %q", s, RolePrimary, RoleStandby)
	}
}

// String returns the process role.
func (r ProcessRole) String() string { return string(r) }

// Valid reports whether r is a defined process role.
func (r ProcessRole) Valid() bool { return r == RolePrimary || r == RoleStandby }

// OperationalName returns "<machine>/<role>" for logs and diagnostics.
//
// It is never a domain identity. Registration proposal, decision, and
// durable-handler identities stay machine-scoped, so two processes on one machine
// never become two registration voters.
func OperationalName(machine string, role ProcessRole) string {
	return machine + "/" + string(role)
}
