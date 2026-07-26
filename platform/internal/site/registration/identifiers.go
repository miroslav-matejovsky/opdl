package registration

import (
	"encoding/hex"
	"slices"
	"strconv"

	"github.com/miroslav-matejovsky/opdl/utils/stablehash"
)

// This file derives the deterministic identities the registration events carry.
// They are what makes retries and redeliveries idempotent: the same fact
// restated from the same inputs always produces the same identity, so a reader
// collapses the repetition instead of counting it twice. They are inputs to the
// events in events.go, not events themselves.

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
