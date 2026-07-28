package redundancy

import "github.com/miroslav-matejovsky/opdl/platform/internal/events"

// This file declares ownership and activation facts. The catalog spans instance
// and machine scopes:
//
//	| Event                | Scope    | Why                                     |
//	| -------------------- | -------- | --------------------------------------- |
//	| lease_opened         | instance | this process opened a file              |
//	| ownership_waiting    | instance | a passive process is waiting            |
//	| promotion_declined   | instance | a passive process decided not to act    |
//	| lease_renewal_failed | instance | one failed attempt; ownership held      |
//	| ownership_acquired   | machine  | which instance owns the machine changed |
//	| stepped_down         | machine  | it changed back                         |
//	| failback_initiated   | machine  | it is changing back                     |
//	| activation_started   | machine  | which instance serves the machine       |
//	| activation_failed    | machine  | the machine has no serving instance     |
//	| activation_completed | machine  | it gave the machine back                |
//
// Scope follows who owns the fact. Process attempts remain instance-scoped;
// ownership and activation transitions outlive a process and belong to the
// machine. Every event also reaches the stating instance's event log.

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

// OwnershipWaiting states that the other instance holds a valid lease, so this
// one remains Passive and waits.
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

// Scope reports that the machine owns the fact: which of its two instances
// holds Primary Ownership is the machine's state, not this process's.
func (OwnershipAcquired) Scope() events.Scope { return events.ScopeMachine }

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

// Scope reports that the machine owns the fact: it is the other half of an
// ownership handover, and a reader of only one of the two would see a machine
// with two owners or none.
func (SteppedDown) Scope() events.Scope { return events.ScopeMachine }

// FailbackInitiated states that an Active Standby has begun handing ownership
// back to a returning healthy Primary under the Preferred Primary policy.
type FailbackInitiated struct{}

// EventType returns the event's stable dotted kind.
func (FailbackInitiated) EventType() events.Type { return TypeFailbackInitiated }

// Scope reports that the machine owns the fact: the owner has begun moving
// ownership, which is the machine's business and not one process's.
func (FailbackInitiated) Scope() events.Scope { return events.ScopeMachine }

// ActivationStarted states that the active composition began.
type ActivationStarted struct {
	// Kind is why this instance became active.
	Kind ActivationKind `json:"activation_kind"`
}

// EventType returns the event's stable dotted kind.
func (ActivationStarted) EventType() events.Type { return TypeActivationStarted }

// Scope reports that the machine owns the fact: which instance is serving is
// what the machine's other instance must not contradict.
func (ActivationStarted) Scope() events.Scope { return events.ScopeMachine }

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

// Scope reports that the machine owns the fact: an activation that failed
// leaves the machine without a serving instance, which is the machine's state.
func (ActivationFailed) Scope() events.Scope { return events.ScopeMachine }

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

// Scope reports that the machine owns the fact: the instance handed the machine
// back, so the machine is free for the other one to take.
func (ActivationCompleted) Scope() events.Scope { return events.ScopeMachine }
