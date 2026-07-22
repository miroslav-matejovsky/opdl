package redundancy

import "github.com/miroslav-matejovsky/opdl/platform/internal/events"

// This file is the redundancy domain's event catalog: what an operator is told
// about which instance of a machine may run, and how it came to be that one.
//
//   - platform.redundancy.lock_opened: this process opened the machine's
//     ownership object.
//   - platform.redundancy.ownership_waiting: the other instance holds ownership,
//     so this one runs passive and waits.
//   - platform.redundancy.ownership_acquired: this process now holds Primary
//     Ownership, either handed over or taken from a process that died.
//   - platform.redundancy.activation_started: the active composition began.
//   - platform.redundancy.activation_failed: it did not complete.
//   - platform.redundancy.activation_completed: it ran and gave ownership back.
//
// They are stated through the events.Publisher Contend is given. Runtime
// composition hands it the process-local one, whose only backend is the local
// JSONL record: a machine contends for ownership before it has a journal to
// write to, and an instance that never becomes active never gets one at all.

const (
	// TypeLockOpened is stated when this process opens the ownership object.
	TypeLockOpened events.Type = "platform.redundancy.lock_opened"
	// TypeOwnershipWaiting is stated when the other instance holds ownership.
	TypeOwnershipWaiting events.Type = "platform.redundancy.ownership_waiting"
	// TypeOwnershipAcquired is stated when this process takes Primary Ownership.
	TypeOwnershipAcquired events.Type = "platform.redundancy.ownership_acquired"
	// TypeActivationStarted is stated when the active composition begins.
	TypeActivationStarted events.Type = "platform.redundancy.activation_started"
	// TypeActivationFailed is stated when the active composition fails.
	TypeActivationFailed events.Type = "platform.redundancy.activation_failed"
	// TypeActivationCompleted is stated when the active composition returns.
	TypeActivationCompleted events.Type = "platform.redundancy.activation_completed"
)

// LockOpened states that this process opened the machine's ownership object.
type LockOpened struct {
	// Object is the named kernel object. It has no path, so this is what
	// identifies it to an operator.
	Object string `json:"object"`
	// Existed reports whether a peer process on this machine already had the
	// object open. Both processes must report the same object; a machine whose
	// two processes report different ones was built from mismatched packages.
	Existed bool `json:"existed"`
}

// EventType returns the event's stable dotted kind.
func (LockOpened) EventType() events.Type { return TypeLockOpened }

// OwnershipWaiting states that the machine's other instance holds Primary
// Ownership, so this one follows the journal and waits for it.
type OwnershipWaiting struct {
	// Object is the ownership object this instance is waiting on.
	Object string `json:"object"`
}

// EventType returns the event's stable dotted kind.
func (OwnershipWaiting) EventType() events.Type { return TypeOwnershipWaiting }

// OwnershipAcquired states that this process holds Primary Ownership, and how it
// obtained it.
//
// Abandoned is the point of the event. An abandoned ownership means the previous
// owner died rather than handed over. The file-lock ownership this replaced could
// not tell the two apart, so an operator had to correlate logs to answer whether
// a failover was planned.
type OwnershipAcquired struct {
	// Object is the ownership object this instance now holds.
	Object string `json:"object"`
	// Abandoned reports that ownership was taken from a process that died
	// without releasing it, rather than from one that handed it over.
	Abandoned bool `json:"abandoned"`
}

// EventType returns the event's stable dotted kind.
func (OwnershipAcquired) EventType() events.Type { return TypeOwnershipAcquired }

// Severity reports a takeover from a dead process as a degradation: ownership is
// equally valid either way, but a machine that lost an instance is worth looking
// at. A clean handover is routine.
func (e OwnershipAcquired) Severity() events.Severity {
	if e.Abandoned {
		return events.SeverityWarn
	}
	return events.SeverityInfo
}

// ActivationStarted states that the active composition began.
type ActivationStarted struct {
	// Kind is why this instance became active.
	Kind ActivationKind `json:"activation_kind"`
}

// EventType returns the event's stable dotted kind.
func (ActivationStarted) EventType() events.Type { return TypeActivationStarted }

// ActivationFailed states that the active composition did not complete.
type ActivationFailed struct {
	// Kind is why this instance became active.
	Kind ActivationKind `json:"activation_kind"`
	// DurationMS is how long the instance was active before it failed.
	DurationMS int64 `json:"duration_ms"`
	// Error is what went wrong.
	Error string `json:"error"`
}

// EventType returns the event's stable dotted kind.
func (ActivationFailed) EventType() events.Type { return TypeActivationFailed }

// Severity reports a machine whose active instance failed as an error.
func (ActivationFailed) Severity() events.Severity { return events.SeverityError }

// ActivationCompleted states that the active composition returned and ownership
// went back.
type ActivationCompleted struct {
	// Kind is why this instance became active.
	Kind ActivationKind `json:"activation_kind"`
	// DurationMS is how long this instance held ownership and served.
	DurationMS int64 `json:"duration_ms"`
}

// EventType returns the event's stable dotted kind.
func (ActivationCompleted) EventType() events.Type { return TypeActivationCompleted }
