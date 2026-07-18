package redundancy

import "slices"

// State is a slot's lifecycle state, used by runtime code and local diagnostics.
// A slot moves through these states in order; only the active state owns the
// machine's externally visible, decision-producing capabilities.
type State string

const (
	// StateStarting is the initial state: the process has launched but has not yet
	// begun to contend for the machine fence.
	StateStarting State = "starting"
	// StateStandby is a slot connected to the journal with caught-up local
	// projections but no public listener, durable domain handlers, or embedded
	// server. A standby waits for the machine fence and produces no domain
	// decision.
	StateStandby State = "standby"
	// StateActivating is a slot that has acquired the fence and is composing its
	// active resources, but is not yet serving.
	StateActivating State = "activating"
	// StateActive is the slot holding the machine's active fence. It owns the
	// public API, the durable domain handlers, readiness publication, and the
	// embedded NATS server.
	StateActive State = "active"
	// StateStopping is a slot releasing its resources during a clean shutdown,
	// before it releases the fence.
	StateStopping State = "stopping"
	// StateFailed is a slot that stopped on an error and is not active.
	StateFailed State = "failed"
)

// transitions is the legal slot lifecycle graph: for each state, the states a
// slot may move to next. A slot may fail from any non-terminal state, and may
// begin stopping from any running state. There is no path back into active from
// stopping or failed: a slot that gave up its active resources starts a new
// process to become active again.
var transitions = map[State][]State{
	StateStarting:   {StateStandby, StateActivating, StateStopping, StateFailed},
	StateStandby:    {StateActivating, StateStopping, StateFailed},
	StateActivating: {StateActive, StateStopping, StateFailed},
	StateActive:     {StateStopping, StateFailed},
	StateStopping:   {StateFailed},
	StateFailed:     nil,
}

// String returns the state's token.
func (s State) String() string { return string(s) }

// Valid reports whether s is one of the defined lifecycle states.
func (s State) Valid() bool {
	_, ok := transitions[s]
	return ok
}

// Active reports whether s is the one state that owns the machine's active,
// externally visible, decision-producing capabilities.
func (s State) Active() bool { return s == StateActive }

// CanTransition reports whether a slot may move directly from s to next. It is
// how runtime code guards a state change so a slot cannot, for example, jump from
// standby to active without composing its active resources first.
func (s State) CanTransition(next State) bool {
	return slices.Contains(transitions[s], next)
}
