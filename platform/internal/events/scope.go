package events

// Scope is the hierarchy level that owns a fact. Every scope reaches the
// stating instance's event log. Machine scope also reaches the machine store;
// site scope is reserved for site distribution. The closed set prevents events
// from declaring a destination no backend understands.
type Scope string

const (
	// ScopeSite states a fact the whole site owns. Every machine is entitled to
	// see it.
	ScopeSite Scope = "site"
	// ScopeMachine states a fact about one machine that outlives the process
	// that stated it, such as which of its instances took Primary Ownership.
	ScopeMachine Scope = "machine"
	// ScopeInstance states a fact about one running process: what it started,
	// bound, waited for, and stopped.
	ScopeInstance Scope = "instance"
)

// DefaultScope is stamped on an event that does not declare its own. Instance
// keeps an omitted declaration local.
const DefaultScope = ScopeInstance

// Valid reports whether s is one of the three defined scopes.
func (s Scope) Valid() bool {
	switch s {
	case ScopeSite, ScopeMachine, ScopeInstance:
		return true
	default:
		return false
	}
}
