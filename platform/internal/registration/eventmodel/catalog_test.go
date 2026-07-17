package eventmodel

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// identity is a complete proposal identity the ID tests vary one field of.
func identity() ProposalIdentity {
	return ProposalIdentity{
		UnitType:               7,
		UnitID:                 42,
		UnitTypeNameAdvertised: "Worker",
		Role:                   "primary",
		OriginMachine:          "node-a",
		OriginIP:               "10.0.1.10",
		ExpectedMachines:       []string{"node-a", "node-b"},
	}
}

func TestNewProposalIDIsDeterministic(t *testing.T) {
	require.Equal(t, NewProposalID(identity()), NewProposalID(identity()))
}

func TestNewProposalIDIgnoresExpectedMachineOrder(t *testing.T) {
	ordered := identity()
	shuffled := identity()
	shuffled.ExpectedMachines = []string{"node-b", "node-a"}
	require.Equal(t, NewProposalID(ordered), NewProposalID(shuffled),
		"the expected set, not its order, is part of a proposal's identity")
}

func TestNewProposalIDChangesWithEveryClaimField(t *testing.T) {
	base := NewProposalID(identity())

	tests := []struct {
		name   string
		mutate func(*ProposalIdentity)
	}{
		{name: "unit type", mutate: func(i *ProposalIdentity) { i.UnitType = 8 }},
		{name: "unit id", mutate: func(i *ProposalIdentity) { i.UnitID = 43 }},
		{name: "advertised name", mutate: func(i *ProposalIdentity) { i.UnitTypeNameAdvertised = "Other" }},
		{name: "role", mutate: func(i *ProposalIdentity) { i.Role = "secondary" }},
		{name: "empty role differs from a set one", mutate: func(i *ProposalIdentity) { i.Role = "" }},
		{name: "origin machine", mutate: func(i *ProposalIdentity) { i.OriginMachine = "node-b" }},
		{name: "origin ip", mutate: func(i *ProposalIdentity) { i.OriginIP = "10.0.1.11" }},
		{name: "expected set", mutate: func(i *ProposalIdentity) { i.ExpectedMachines = []string{"node-a", "node-b", "node-c"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := identity()
			test.mutate(&mutated)
			require.NotEqual(t, base, NewProposalID(mutated))
		})
	}
}

func TestNewProposalIDIsNotForgeableAcrossFieldBoundaries(t *testing.T) {
	// Length prefixing keeps a value from borrowing bytes from the next field: a
	// machine name and the field after it cannot be rearranged into one another.
	left := identity()
	left.OriginMachine = "node"
	left.OriginIP = "a10.0.1.10"
	right := identity()
	right.OriginMachine = "nodea"
	right.OriginIP = "10.0.1.10"
	require.NotEqual(t, NewProposalID(left), NewProposalID(right))
}

func TestNewDecisionIDDistinguishesKindAndMachine(t *testing.T) {
	proposalID := NewProposalID(identity())

	confirmA := NewDecisionID(proposalID, DecisionConfirmed, "node-a")
	require.Equal(t, confirmA, NewDecisionID(proposalID, DecisionConfirmed, "node-a"), "deterministic")
	require.NotEqual(t, confirmA, NewDecisionID(proposalID, DecisionRejected, "node-a"), "kind is part of identity")
	require.NotEqual(t, confirmA, NewDecisionID(proposalID, DecisionConfirmed, "node-b"), "machine is part of identity")

	other := NewProposalID(func() ProposalIdentity { i := identity(); i.UnitID = 99; return i }())
	require.NotEqual(t, confirmA, NewDecisionID(other, DecisionConfirmed, "node-a"), "proposal is part of identity")
}

func TestNewProposedSortsExpectedMachinesAndStampsID(t *testing.T) {
	proposed := NewProposed(ProposalIdentity{
		UnitType:               7,
		UnitID:                 42,
		UnitTypeNameAdvertised: "Worker",
		OriginMachine:          "node-a",
		OriginIP:               "10.0.1.10",
		ExpectedMachines:       []string{"node-c", "node-a", "node-b"},
	})

	require.Equal(t, []string{"node-a", "node-b", "node-c"}, proposed.ExpectedMachines)
	require.NotEmpty(t, proposed.ProposalID)
	require.Equal(t, Key{UnitType: 7, UnitID: 42}, proposed.Key())
}

func TestConstructorsDeriveConsistentIdentities(t *testing.T) {
	proposed := NewProposed(identity())

	confirmed := NewConfirmed(proposed.ProposalID, "node-a")
	require.Equal(t, NewDecisionID(proposed.ProposalID, DecisionConfirmed, "node-a"), confirmed.DecisionID)

	rejected := NewRejected(proposed.ProposalID, "node-b", ReasonKeyConflict)
	require.Equal(t, NewDecisionID(proposed.ProposalID, DecisionRejected, "node-b"), rejected.DecisionID)
	require.Equal(t, ReasonKeyConflict, rejected.Reason)

	accepted := NewAccepted(proposed)
	require.Equal(t, proposed.ProposalID, accepted.ProposalID)
	require.Equal(t, proposed.UnitType, accepted.UnitType)
	require.Equal(t, proposed.OriginMachine, accepted.OriginMachine)
}

func TestCatalogEventsDeclareTheirContract(t *testing.T) {
	var proposed events.Event = Proposed{}
	require.Equal(t, TypeProposed, proposed.EventType())
	require.Equal(t, eventSource, proposed.Source())

	require.Equal(t, TypeConfirmed, Confirmed{}.EventType())
	require.Equal(t, TypeRejected, Rejected{}.EventType())
	require.Equal(t, TypeAccepted, Accepted{}.EventType())

	// Each event declares an explicit, positive schema version.
	require.Equal(t, schemaVersion, Proposed{}.SchemaVersion())
	require.Equal(t, schemaVersion, Confirmed{}.SchemaVersion())
	require.Equal(t, schemaVersion, Rejected{}.SchemaVersion())
	require.Equal(t, schemaVersion, Accepted{}.SchemaVersion())

	// A rejection is an operational anomaly.
	require.Equal(t, []string{events.TagWarning}, Rejected{}.Tags())
}
