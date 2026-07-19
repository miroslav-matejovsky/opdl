package registration

import (
	"encoding/hex"
	"slices"
	"strconv"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/utils/stablehash"
)

// This file is the registration domain's event catalog in the event-sourced
// model: the four facts a registration is expressed as, and the deterministic
// identities that make retries and redeliveries idempotent. Each event is a
// published contract, self-contained enough that a reader reconstructs a
// registration from the journal alone.

// eventSource is the subsystem every event in this catalog comes from.
const eventSource = "registration"

// schemaVersion is the payload schema version stamped into every proposal and
// decision identity and reported by each event. It is part of an identity hash,
// so proposals written by two encodings can never share one ID.
const schemaVersion = 1

// ReasonKeyConflict is the bounded reason a proposal is rejected for losing a
// unit key it did not claim first in journal order.
const ReasonKeyConflict = "registration_key_conflict"

// ReasonInvalidProposal is the bounded reason an expected node rejects a
// proposal that does not match the trusted topology or domain validation.
const ReasonInvalidProposal = "registration_invalid_proposal"

// Event type constants. Each reads as a fact: platform.<domain>.<fact>, past
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

// DecisionKind is the kind of decision one node states about a proposal. It is
// part of a decision identity, so a node's confirmation and its rejection of the
// same proposal are distinct facts that never collapse into each other.
type DecisionKind string

const (
	// DecisionConfirmed is a node's acceptance of a proposal.
	DecisionConfirmed DecisionKind = "confirmed"
	// DecisionRejected is a node's refusal of a proposal.
	DecisionRejected DecisionKind = "rejected"
)

// ProposalIdentity is the canonical set of fields a proposal ID is derived from:
// the versioned request, the trusted origin identity, and the ordered expected
// machines. It is the input to NewProposalID and to NewProposed; it is not
// itself an event.
type ProposalIdentity struct {
	// UnitType is the requested unit type identifier.
	UnitType uint8
	// UnitID is the requested unit identifier within UnitType.
	UnitID uint16
	// UnitTypeNameAdvertised is the unit's advertised type name.
	UnitTypeNameAdvertised string
	// Role is the unit's advertised role, empty when it advertised none.
	Role string
	// OriginMachine is the trusted descriptor machine the proposal originates on.
	OriginMachine string
	// OriginIP is the trusted descriptor IP of that machine.
	OriginIP string
	// ExpectedMachines is the set of machines whose confirmation the proposal
	// needs. It is sorted before it is hashed and stored, so its order never
	// changes a proposal's identity.
	ExpectedMachines []string
}

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

// Source returns the subsystem that states the fact.
func (Proposed) Source() string { return eventSource }

// SchemaVersion returns the proposal payload schema version.
func (Proposed) SchemaVersion() int { return schemaVersion }

// DedupID returns the stable publication identity of this proposal.
func (p Proposed) DedupID() string { return "registration.proposal." + p.ProposalID }

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

// Source returns the subsystem that states the fact.
func (Confirmed) Source() string { return eventSource }

// SchemaVersion returns the decision payload schema version.
func (Confirmed) SchemaVersion() int { return schemaVersion }

// DedupID returns the stable publication identity of this decision.
func (c Confirmed) DedupID() string { return "registration.decision." + c.DecisionID }

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

// Source returns the subsystem that states the fact.
func (Rejected) Source() string { return eventSource }

// SchemaVersion returns the decision payload schema version.
func (Rejected) SchemaVersion() int { return schemaVersion }

// Tags marks the refusal as an operational anomaly.
func (Rejected) Tags() []string { return []string{events.TagWarning} }

// DedupID returns the stable publication identity of this decision.
func (r Rejected) DedupID() string { return "registration.decision." + r.DecisionID }

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

// Source returns the subsystem that states the fact.
func (Accepted) Source() string { return eventSource }

// SchemaVersion returns the acceptance payload schema version.
func (Accepted) SchemaVersion() int { return schemaVersion }

// DedupID returns the stable publication identity of this acceptance.
func (a Accepted) DedupID() string { return "registration.acceptance." + a.ProposalID }

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

// NewProposalID derives a proposal's deterministic identity from its versioned
// canonical fields, trusted origin, and ordered expected machines. Every field
// is length-prefixed so no two different proposals can encode the same bytes.
func NewProposalID(identity ProposalIdentity) string {
	expected := slices.Clone(identity.ExpectedMachines)
	slices.Sort(expected)

	values := make([]string, 0, 8+len(expected))
	values = append(values,
		strconv.Itoa(schemaVersion),
		strconv.FormatUint(uint64(identity.UnitType), 10),
		strconv.FormatUint(uint64(identity.UnitID), 10),
		identity.UnitTypeNameAdvertised,
		identity.Role,
		identity.OriginMachine,
		identity.OriginIP,
		strconv.Itoa(len(expected)),
	)
	values = append(values, expected...)
	sum := stablehash.Sum256(values...)
	return hex.EncodeToString(sum[:])
}

// NewDecisionID derives a decision's deterministic identity from the proposal it
// is about, its kind, and the deciding machine. A node that republishes the same
// decision produces the same ID, so redelivery never doubles a decision.
func NewDecisionID(proposalID string, kind DecisionKind, decidingMachine string) string {
	sum := stablehash.Sum256(proposalID, string(kind), decidingMachine)
	return hex.EncodeToString(sum[:])
}
