package registration

import (
	"testing"

	"github.com/stretchr/testify/require"
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
