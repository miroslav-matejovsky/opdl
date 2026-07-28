package registration

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

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

	require.Equal(t, TypeConfirmed, Confirmed{}.EventType())
	require.Equal(t, TypeRejected, Rejected{}.EventType())
	require.Equal(t, TypeAccepted, Accepted{}.EventType())

	// The source is derived from the type, so no event restates it.
	for _, eventType := range []events.Type{TypeProposed, TypeConfirmed, TypeRejected, TypeAccepted} {
		require.NoError(t, eventType.Validate())
		require.Equal(t, "registration", eventType.Source())
	}

	// Each event declares an explicit, positive schema version.
	require.Equal(t, schemaVersion, Proposed{}.SchemaVersion())
	require.Equal(t, schemaVersion, Confirmed{}.SchemaVersion())
	require.Equal(t, schemaVersion, Rejected{}.SchemaVersion())
	require.Equal(t, schemaVersion, Accepted{}.SchemaVersion())

	// A rejection is an operational anomaly; the other three are routine facts
	// that declare no severity at all.
	require.Equal(t, events.SeverityWarn, Rejected{}.Severity())
	require.NotImplements(t, (*events.Severe)(nil), Accepted{})
	require.NotImplements(t, (*events.Tagged)(nil), Rejected{}, "severity states this, so a tag would repeat it")
}

func TestEventsDeclareStableIdentitiesForIdenticalFacts(t *testing.T) {
	require.Equal(t, "registration.proposal.p", Proposed{ProposalID: "p"}.StableID())
	require.Equal(t, "registration.decision.d", Confirmed{DecisionID: "d"}.StableID())
	require.Equal(t, "registration.decision.d", Rejected{DecisionID: "d"}.StableID())
	require.Equal(t, "registration.acceptance.p", Accepted{ProposalID: "p"}.StableID())

	// The same fact restated from the same inputs keeps its identity, which is
	// what makes a retry or a redelivery collapse instead of doubling.
	proposed := NewProposed(identity())
	require.Equal(t, proposed.StableID(), NewProposed(identity()).StableID())
	require.Equal(t, NewAccepted(proposed).StableID(), NewAccepted(NewProposed(identity())).StableID())

	confirmed := NewConfirmed(proposed.ProposalID, "node-a")
	require.Equal(t, confirmed.StableID(), NewConfirmed(proposed.ProposalID, "node-a").StableID())
	rejected := NewRejected(proposed.ProposalID, "node-a", ReasonKeyConflict)
	require.NotEqual(t, confirmed.StableID(), rejected.StableID(),
		"one machine's confirmation and rejection are distinct facts")
}
