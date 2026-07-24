package redundancy

import "github.com/miroslav-matejovsky/opdl/platform/internal/events"

// This file is the redundancy domain's event catalog: what an operator is told
// about which instance of a machine may run, and how it came to be that one.
//
//   - platform.redundancy.lease_opened: this process opened the machine's
//     ownership lease file.
//   - platform.redundancy.ownership_waiting: the other instance holds a valid
//     lease, so this one runs passive and waits.
//   - platform.redundancy.ownership_acquired: this process took Primary
//     Ownership, either from a lease handed over or one that lapsed.
//   - platform.redundancy.promotion_declined: the lease had lapsed but the peer
//     was still healthy, so this instance did not promote.
//   - platform.redundancy.lease_renewal_failed: an Active instance could not
//     renew its lease but has not yet had to step down.
//   - platform.redundancy.stepped_down: an Active instance stopped being Active
//     because it lost or could no longer keep its lease.
//   - platform.redundancy.failback_initiated: an Active Standby began handing
//     ownership back to a returning healthy Primary.
//   - platform.redundancy.activation_started / _failed / _completed: the active
//     composition began, did not complete, or ran and gave ownership back.
//
// They are stated through the events.Publisher ManageOwnership is given. Runtime
// composition hands it the process-local one, whose only backend is the local
// JSONL record: ownership is decided before a process has a journal to write to,
// and an instance that never becomes active never gets one at all.

const (
	// TypeLeaseOpened is stated when this process opens the ownership lease file.
	TypeLeaseOpened events.Type = "platform.redundancy.lease_opened"
	// TypeOwnershipWaiting is stated when the other instance holds a valid lease.
	TypeOwnershipWaiting events.Type = "platform.redundancy.ownership_waiting"
	// TypeOwnershipAcquired is stated when this process takes Primary Ownership.
	TypeOwnershipAcquired events.Type = "platform.redundancy.ownership_acquired"
	// TypePromotionDeclined is stated when a lapsed lease did not lead to promotion
	// because the peer was still healthy.
	TypePromotionDeclined events.Type = "platform.redundancy.promotion_declined"
	// TypeLeaseRenewalFailed is stated when an owner could not renew its lease.
	TypeLeaseRenewalFailed events.Type = "platform.redundancy.lease_renewal_failed"
	// TypeSteppedDown is stated when an Active instance stops being Active because
	// it lost or could no longer keep its lease.
	TypeSteppedDown events.Type = "platform.redundancy.stepped_down"
	// TypeFailbackInitiated is stated when an Active Standby begins handing
	// ownership back to a returning healthy Primary.
	TypeFailbackInitiated events.Type = "platform.redundancy.failback_initiated"
	// TypeActivationStarted is stated when the active composition begins.
	TypeActivationStarted events.Type = "platform.redundancy.activation_started"
	// TypeActivationFailed is stated when the active composition fails.
	TypeActivationFailed events.Type = "platform.redundancy.activation_failed"
	// TypeActivationCompleted is stated when the active composition returns.
	TypeActivationCompleted events.Type = "platform.redundancy.activation_completed"
)

// LeaseOpened states that this process opened the machine's ownership lease file.
type LeaseOpened struct {
	// File is the machine-wide lease file ownership is recorded in.
	File string `json:"file"`
}

// EventType returns the event's stable dotted kind.
func (LeaseOpened) EventType() events.Type { return TypeLeaseOpened }

// OwnershipWaiting states that the machine's other instance holds a valid lease,
// so this one follows the journal and waits for it.
type OwnershipWaiting struct {
	// File is the ownership lease this instance is waiting on.
	File string `json:"file"`
}

// EventType returns the event's stable dotted kind.
func (OwnershipWaiting) EventType() events.Type { return TypeOwnershipWaiting }

// OwnershipAcquired states that this process holds Primary Ownership and how it
// obtained it.
//
// Abandoned is the point of the event. An abandoned lease means the previous owner
// died and let its lease lapse rather than handing it over, so an operator can
// tell a planned handover from a failover without correlating logs.
type OwnershipAcquired struct {
	// File is the ownership lease this instance now holds.
	File string `json:"file"`
	// Abandoned reports that ownership was taken from a lease that lapsed without a
	// clean release, rather than from one handed over.
	Abandoned bool `json:"abandoned"`
}

// EventType returns the event's stable dotted kind.
func (OwnershipAcquired) EventType() events.Type { return TypeOwnershipAcquired }

// Severity reports a takeover from a lapsed lease as a degradation: ownership is
// equally valid either way, but a machine that lost an instance is worth looking
// at. A clean handover is routine.
func (e OwnershipAcquired) Severity() events.Severity {
	if e.Abandoned {
		return events.SeverityWarn
	}
	return events.SeverityInfo
}

// PromotionDeclined states that a Passive instance evaluated promotion, found the
// lease lapsed, but did not promote because the peer was still healthy.
type PromotionDeclined struct {
	// Reason is why promotion was declined.
	Reason string `json:"reason"`
}

// EventType returns the event's stable dotted kind.
func (PromotionDeclined) EventType() events.Type { return TypePromotionDeclined }

// LeaseRenewalFailed states that an owner could not renew its lease on one
// attempt. It is a warning rather than an error: the lease is not yet lost, and
// renewal will be retried before the step-down grace runs out.
type LeaseRenewalFailed struct {
	// Error is what the renewal attempt failed with.
	Error string `json:"error"`
}

// EventType returns the event's stable dotted kind.
func (LeaseRenewalFailed) EventType() events.Type { return TypeLeaseRenewalFailed }

// Severity reports a failed renewal as a warning: recoverable until the lease
// actually lapses.
func (LeaseRenewalFailed) Severity() events.Severity { return events.SeverityWarn }

// SteppedDown states that an Active instance stopped being Active because it lost
// its lease to a takeover or could no longer keep it alive.
type SteppedDown struct {
	// Reason is why the instance stepped down.
	Reason string `json:"reason"`
}

// EventType returns the event's stable dotted kind.
func (SteppedDown) EventType() events.Type { return TypeSteppedDown }

// Severity reports a step-down as a warning: the machine failed over, which is
// worth an operator's attention but is the redundancy working, not breaking.
func (SteppedDown) Severity() events.Severity { return events.SeverityWarn }

// FailbackInitiated states that an Active Standby has begun handing ownership
// back to a returning healthy Primary under the Preferred Primary policy.
type FailbackInitiated struct{}

// EventType returns the event's stable dotted kind.
func (FailbackInitiated) EventType() events.Type { return TypeFailbackInitiated }

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
