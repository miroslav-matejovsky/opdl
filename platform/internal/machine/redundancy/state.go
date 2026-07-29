package redundancy

// State is what an instance is doing now, as opposed to InstanceRole, which is
// which instance it is. The two axes are independent: a Standby Instance in
// StateActive is a machine that has failed over.
//
// Only StateActive owns the machine's externally visible, decision-producing
// capabilities. The Passive instance answers for itself and produces no domain
// decision.
//
// There are two states because there are two things an instance can be doing.
// The type used to carry four more — starting, activating, stopping, failed — so
// that a status file could report which one an instance was in. That file is
// gone: each of those moments is now a fact of its own in the local record,
// stated by whichever component makes the move, so a transitional state has a
// better description than a token in a snapshot. See ownership.go for the
// ownership half and the runtime's event catalog for the composition half.
type State string

const (
	// StatePassive is a process with no durable domain handlers and no
	// domain-serving API. It hosts its instance's authored Event Fabric server
	// and answers about itself. A passive instance waits for Primary Ownership
	// and produces no domain decision.
	StatePassive State = "passive"
	// StateActive is the process holding Primary Ownership. It owns domain API
	// serving, durable domain handlers, and readiness publication.
	StateActive State = "active"
)

// String returns the state's token. It is the token the platform API answers
// with and the one the local record carries, so the three never disagree.
func (s State) String() string { return string(s) }

// Valid reports whether s is one of the defined states.
func (s State) Valid() bool { return s == StatePassive || s == StateActive }

// Active reports whether s is the one state that owns the machine's active,
// externally visible, decision-producing capabilities.
func (s State) Active() bool { return s == StateActive }
