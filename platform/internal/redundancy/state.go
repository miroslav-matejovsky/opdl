package redundancy

import "slices"

// State is what an instance is doing now, as opposed to InstanceRole, which is
// which instance it is. The two axes are independent: a Standby Instance in
// StateActive is a machine that has failed over.
//
// StateActive and StatePassive are the operational states. The rest are
// transitional and exist so an operator can tell a process that has not yet
// contended from one that contended and lost, and a clean stop from a failure.
//
// Only StateActive owns the machine's externally visible, decision-producing
// capabilities.
type State string

const (
	// StateStarting is the initial state: the process has launched but has not yet
	// begun to contend for Primary Ownership.
	StateStarting State = "starting"
	// StatePassive is a process connected to the journal with caught-up local
	// projections but no public listener, durable domain handlers, or embedded
	// server. A passive instance waits for the machine fence and produces no domain
	// decision.
	StatePassive State = "passive"
	// StateActivating is a process that has acquired the fence and is composing its
	// active resources, but is not yet serving.
	StateActivating State = "activating"
	// StateActive is the process holding the machine's active fence. It owns the
	// public API, the durable domain handlers, readiness publication, and the
	// embedded NATS server.
	StateActive State = "active"
	// StateStopping is a process releasing its resources during a clean shutdown,
	// before it releases the fence.
	StateStopping State = "stopping"
	// StateFailed is a process that stopped on an error and is not active.
	StateFailed State = "failed"
)

// transitions is the legal process lifecycle graph. A process may fail from any
// non-terminal state and may
// begin stopping from any running state. There is no path back into active from
// stopping or failed: a process that gave up its active resources starts a new
// process to become active again.
var transitions = map[State][]State{
	StateStarting:   {StatePassive, StateActivating, StateStopping, StateFailed},
	StatePassive:    {StateActivating, StateStopping, StateFailed},
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

// CanTransition reports whether a process may move directly from s to next. It is
// how runtime code guards a state change so a process cannot, for example, jump from
// passive to active without composing its active resources first.
func (s State) CanTransition(next State) bool {
	return slices.Contains(transitions[s], next)
}
