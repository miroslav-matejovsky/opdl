package events

// Scope is the level of the hierarchy that owns a fact: the site, one machine,
// or one instance. It is stamped on every envelope and is what a reader uses to
// tell "this deployment decided something" from "this process did something".
//
// Scope decides where an event travels beyond the instance that stated it. It
// never decides whether an event is recorded: every event a process states is
// appended to that instance's local record whatever its scope, because the
// record is the operator's log of everything that happened on the instance. A
// wider scope adds destinations, it does not move the fact out of the log.
//
//   - ScopeInstance stays in the local record.
//   - ScopeMachine also reaches the machine's shared store, so both instances of
//     one machine read the same fact.
//   - ScopeSite is also distributed to every machine in the site.
//
// The set is closed for the same reason severity's is: routing is decided by
// matching against these three, so a writer cannot invent a fourth level that
// nothing knows how to deliver.
type Scope string

const (
	// ScopeSite states a fact the whole site owns, such as a registration
	// decision. Every machine is entitled to see it.
	ScopeSite Scope = "site"
	// ScopeMachine states a fact about one machine that outlives the process
	// that stated it, such as which of its instances took Primary Ownership.
	ScopeMachine Scope = "machine"
	// ScopeInstance states a fact about one running process: what it started,
	// bound, waited for, and stopped.
	ScopeInstance Scope = "instance"
)

// DefaultScope is stamped on an event that does not declare its own.
//
// Instance is the safe direction to be wrong in. A forgotten declaration keeps
// a fact local, where an operator still finds it, instead of publishing it to
// every machine in the site. The cost is that a genuinely wider fact can go
// unnoticed, which is why each catalog test asserts the scope of every event it
// declares rather than trusting the default.
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
