package registration

import (
	"slices"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// This file is the registration domain's event catalog: every fact this package
// can state, and nothing else. Each event is a published contract,
// self-contained enough that a reader reconstructs a registration from the
// journal alone.
//
//   - platform.registration.proposed: the origin proposes to register a unit.
//   - platform.registration.confirmed: one expected node accepts the claiming
//     proposal.
//   - platform.registration.rejected: one expected node refuses a proposal, or
//     a proposal loses its key to an earlier one.
//   - platform.registration.accepted: the origin commits a fully confirmed
//     proposal.
//
// The deterministic identities these payloads carry are derived in
// identifiers.go.

// schemaVersion is the payload schema version every event in this catalog
// reports. It is also stamped into a proposal identity, so proposals written by
// two encodings can never share one ID.
const schemaVersion = 1

// ReasonKeyConflict is the bounded reason a proposal is rejected for losing a
// unit key it did not claim first in journal order.
const ReasonKeyConflict = "registration_key_conflict"

// ReasonInvalidProposal is the bounded reason an expected node rejects a
// proposal that does not match the trusted topology or domain validation.
const ReasonInvalidProposal = "registration_invalid_proposal"

// Event type constants. Each reads as a fact: platform.<source>.<fact>, past
// tense.
const (
	// TypeProposed is stated when the origin proposes a registration.
	TypeProposed events.Type = "platform.registration.proposed"
	// TypeConfirmed is stated when one expected node accepts the claiming proposal.
	TypeConfirmed events.Type = "platform.registration.confirmed"
	// TypeRejected is stated when one expected node refuses a proposal, or a
	// proposal loses its key to an earlier one.
	TypeRejected events.Type = "platform.registration.rejected"
	// TypeAccepted is stated when the origin commits a fully confirmed proposal.
	TypeAccepted events.Type = "platform.registration.accepted"
)

// Proposed states the origin's proposal to register a unit. It carries the
// complete request, the trusted origin, the ordered expected machines, and the
// proposal ID derived from all of them, so a reader needs nothing else to judge
// or replay it.
type Proposed struct {
	// ProposalID is the deterministic identity of this proposal.
	ProposalID string `json:"proposal_id"`
	// UnitType is the requested unit type identifier.
	UnitType uint8 `json:"unit_type"`
	// UnitID is the requested unit identifier within UnitType.
	UnitID uint16 `json:"unit_id"`
	// UnitTypeNameAdvertised is the unit's advertised type name.
	UnitTypeNameAdvertised string `json:"unit_type_name_advertised"`
	// Role is the unit's advertised role, omitted when it advertised none.
	Role string `json:"role,omitempty"`
	// OriginMachine is the trusted descriptor machine the proposal originates on.
	OriginMachine string `json:"origin_machine"`
	// OriginIP is the trusted descriptor IP of that machine.
	OriginIP string `json:"origin_ip"`
	// ExpectedMachines is the sorted set of machines that must confirm.
	ExpectedMachines []string `json:"expected_machines"`
}

// EventType returns the event's stable dotted kind.
func (Proposed) EventType() events.Type { return TypeProposed }

// SchemaVersion returns the proposal payload schema version.
func (Proposed) SchemaVersion() int { return schemaVersion }

// StableID returns the identity that makes a restated proposal the same fact.
func (p Proposed) StableID() string { return "registration.proposal." + p.ProposalID }

// Key is the unit key this proposal is for.
func (p Proposed) Key() Key { return Key{UnitType: p.UnitType, UnitID: p.UnitID} }

// Confirmed states one expected node's acceptance of the claiming proposal.
type Confirmed struct {
	// DecisionID is the deterministic identity of this decision.
	DecisionID string `json:"decision_id"`
	// ProposalID is the proposal this decision is about.
	ProposalID string `json:"proposal_id"`
	// DecidingMachine is the expected node that made the decision.
	DecidingMachine string `json:"deciding_machine"`
}

// EventType returns the event's stable dotted kind.
func (Confirmed) EventType() events.Type { return TypeConfirmed }

// SchemaVersion returns the decision payload schema version.
func (Confirmed) SchemaVersion() int { return schemaVersion }

// StableID returns the identity that makes a restated decision the same fact.
func (c Confirmed) StableID() string { return "registration.decision." + c.DecisionID }

// Rejected states one expected node's refusal of a proposal, or a proposal
// losing its key to an earlier one.
type Rejected struct {
	// DecisionID is the deterministic identity of this decision.
	DecisionID string `json:"decision_id"`
	// ProposalID is the proposal this decision is about.
	ProposalID string `json:"proposal_id"`
	// DecidingMachine is the expected node that made the decision.
	DecidingMachine string `json:"deciding_machine"`
	// Reason is the bounded machine-readable rejection code.
	Reason string `json:"reason"`
}

// EventType returns the event's stable dotted kind.
func (Rejected) EventType() events.Type { return TypeRejected }

// SchemaVersion returns the decision payload schema version.
func (Rejected) SchemaVersion() int { return schemaVersion }

// Severity marks the refusal as an operational anomaly rather than a routine
// transition: a registration that does not happen is what an operator looks for.
func (Rejected) Severity() events.Severity { return events.SeverityWarn }

// StableID returns the identity that makes a restated decision the same fact.
func (r Rejected) StableID() string { return "registration.decision." + r.DecisionID }

// Accepted states the origin's commit of a fully confirmed proposal. It repeats
// the committed registration fields so the acceptance stands alone.
type Accepted struct {
	// ProposalID is the proposal that was accepted.
	ProposalID string `json:"proposal_id"`
	// UnitType is the registered unit type identifier.
	UnitType uint8 `json:"unit_type"`
	// UnitID is the registered unit identifier within UnitType.
	UnitID uint16 `json:"unit_id"`
	// UnitTypeNameAdvertised is the unit's advertised type name.
	UnitTypeNameAdvertised string `json:"unit_type_name_advertised"`
	// Role is the unit's advertised role, omitted when it advertised none.
	Role string `json:"role,omitempty"`
	// OriginMachine is the trusted descriptor machine the proposal originated on.
	OriginMachine string `json:"origin_machine"`
	// OriginIP is the trusted descriptor IP of that machine.
	OriginIP string `json:"origin_ip"`
}

// EventType returns the event's stable dotted kind.
func (Accepted) EventType() events.Type { return TypeAccepted }

// SchemaVersion returns the acceptance payload schema version.
func (Accepted) SchemaVersion() int { return schemaVersion }

// StableID returns the identity that makes a restated acceptance the same fact.
func (a Accepted) StableID() string { return "registration.acceptance." + a.ProposalID }

// NewProposed builds a proposal from its identity. It sorts the expected
// machines so the same set always produces the same proposal, and stamps the
// derived proposal ID.
func NewProposed(identity ProposalIdentity) Proposed {
	expected := slices.Clone(identity.ExpectedMachines)
	slices.Sort(expected)
	identity.ExpectedMachines = expected
	return Proposed{
		ProposalID:             NewProposalID(identity),
		UnitType:               identity.UnitType,
		UnitID:                 identity.UnitID,
		UnitTypeNameAdvertised: identity.UnitTypeNameAdvertised,
		Role:                   identity.Role,
		OriginMachine:          identity.OriginMachine,
		OriginIP:               identity.OriginIP,
		ExpectedMachines:       expected,
	}
}

// NewConfirmed builds one node's confirmation of a proposal, with its derived
// decision ID.
func NewConfirmed(proposalID, decidingMachine string) Confirmed {
	return Confirmed{
		DecisionID:      NewDecisionID(proposalID, DecisionConfirmed, decidingMachine),
		ProposalID:      proposalID,
		DecidingMachine: decidingMachine,
	}
}

// NewRejected builds one node's rejection of a proposal, with its derived
// decision ID.
func NewRejected(proposalID, decidingMachine, reason string) Rejected {
	return Rejected{
		DecisionID:      NewDecisionID(proposalID, DecisionRejected, decidingMachine),
		ProposalID:      proposalID,
		DecidingMachine: decidingMachine,
		Reason:          reason,
	}
}

// NewAccepted builds the origin's acceptance of a claiming proposal, copying the
// committed fields from it.
func NewAccepted(proposal Proposed) Accepted {
	return Accepted{
		ProposalID:             proposal.ProposalID,
		UnitType:               proposal.UnitType,
		UnitID:                 proposal.UnitID,
		UnitTypeNameAdvertised: proposal.UnitTypeNameAdvertised,
		Role:                   proposal.Role,
		OriginMachine:          proposal.OriginMachine,
		OriginIP:               proposal.OriginIP,
	}
}
