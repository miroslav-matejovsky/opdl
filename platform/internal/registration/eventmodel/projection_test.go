package eventmodel

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/eventfabric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// apply folds one event into p at the given journal sequence, through the same
// decode-and-dispatch path the Event Fabric drives.
func apply(t *testing.T, p *Projection, sequence uint64, event events.Event) {
	t.Helper()
	data, err := json.Marshal(event)
	require.NoError(t, err)
	machine := "node-a"
	switch value := event.(type) {
	case Proposed:
		machine = value.OriginMachine
	case Confirmed:
		machine = value.DecidingMachine
	case Rejected:
		machine = value.DecidingMachine
	case Accepted:
		machine = value.OriginMachine
	}
	delivery := eventfabric.Delivery{
		Record: events.Record{Meta: events.Meta{
			ID:            "event-id",
			Type:          event.EventType(),
			SchemaVersion: 1,
			Node:          events.Node{Machine: machine},
		}, Data: data},
		Sequence: sequence,
	}
	require.NoError(t, p.Apply(t.Context(), delivery))
}

// proposal builds a node-a proposal for a unit with a two-node expected set.
func proposal(unitID uint16) Proposed {
	return NewProposed(ProposalIdentity{
		UnitType:               7,
		UnitID:                 unitID,
		UnitTypeNameAdvertised: "Worker",
		OriginMachine:          "node-a",
		OriginIP:               "10.0.1.10",
		ExpectedMachines:       []string{"node-a", "node-b"},
	})
}

func TestProjectionFirstProposalInJournalOrderClaimsTheKey(t *testing.T) {
	p := NewProjection()
	first := proposal(42)
	second := NewProposed(ProposalIdentity{
		UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Worker",
		OriginMachine: "node-b", OriginIP: "10.0.1.11",
		ExpectedMachines: []string{"node-a", "node-b"},
	})

	apply(t, p, 1, first)
	apply(t, p, 2, second)

	selected, ok := p.SelectedProposal(Key{UnitType: 7, UnitID: 42})
	require.True(t, ok)
	require.Equal(t, first.ProposalID, selected, "the earliest proposal in journal order claims the key")

	firstStatus, _ := p.ProposalStatus(first.ProposalID)
	require.Equal(t, StatusPending, firstStatus)
	secondStatus, _ := p.ProposalStatus(second.ProposalID)
	require.Equal(t, StatusRejected, secondStatus, "a later different proposal loses the key")
}

func TestProjectionIdenticalProposalIsAnIdempotentRetry(t *testing.T) {
	p := NewProjection()
	proposed := proposal(42)

	apply(t, p, 1, proposed)
	apply(t, p, 2, proposed) // an exact retry, redelivered at a later sequence

	// The retry adds no contender, so the key has no conflict.
	require.Empty(t, p.Conflicts())
	selected, ok := p.SelectedProposal(proposed.Key())
	require.True(t, ok)
	require.Equal(t, proposed.ProposalID, selected)
}

func TestProjectionCollapsesDuplicateDecisions(t *testing.T) {
	p := NewProjection()
	proposed := proposal(42)
	confirmA := NewConfirmed(proposed.ProposalID, "node-a")

	apply(t, p, 1, proposed)
	apply(t, p, 2, confirmA)
	apply(t, p, 3, confirmA) // redelivery of the same decision

	require.Equal(t, []string{"node-a"}, p.Confirmations(proposed.ProposalID),
		"a redelivered decision collapses onto the one decision already made")
}

func TestProjectionAcceptsAfterEveryExpectedNodeConfirms(t *testing.T) {
	p := NewProjection()
	proposed := proposal(42)

	apply(t, p, 1, proposed)
	require.False(t, p.AllExpectedConfirmed(proposed.ProposalID))

	apply(t, p, 2, NewConfirmed(proposed.ProposalID, "node-a"))
	require.False(t, p.AllExpectedConfirmed(proposed.ProposalID), "one of two expected nodes has confirmed")

	apply(t, p, 3, NewConfirmed(proposed.ProposalID, "node-b"))
	require.True(t, p.AllExpectedConfirmed(proposed.ProposalID), "every expected node has confirmed")
	require.Equal(t, []string{"node-a", "node-b"}, p.Confirmations(proposed.ProposalID))

	// The origin's acceptance commits it; only then is the status accepted.
	status, _ := p.ProposalStatus(proposed.ProposalID)
	require.Equal(t, StatusPending, status, "acceptance is a fact, not derived from confirmations")

	apply(t, p, 4, NewAccepted(proposed))
	require.True(t, p.IsAccepted(proposed.ProposalID))
	status, _ = p.ProposalStatus(proposed.ProposalID)
	require.Equal(t, StatusAccepted, status)
}

func TestProjectionAnExpectedRejectionRejectsTheProposal(t *testing.T) {
	p := NewProjection()
	proposed := proposal(42)

	apply(t, p, 1, proposed)
	apply(t, p, 2, NewConfirmed(proposed.ProposalID, "node-a"))
	apply(t, p, 3, NewRejected(proposed.ProposalID, "node-b", "unexpected_machine"))

	status, _ := p.ProposalStatus(proposed.ProposalID)
	require.Equal(t, StatusRejected, status)
	require.Equal(t, map[string]string{"node-b": "unexpected_machine"}, p.Rejections(proposed.ProposalID))
	require.False(t, p.AllExpectedConfirmed(proposed.ProposalID))
}

func TestProjectionResolvesAConflictToTheJournalOrderWinner(t *testing.T) {
	p := NewProjection()
	winner := proposal(42)
	loser := NewProposed(ProposalIdentity{
		UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Worker",
		OriginMachine: "node-b", OriginIP: "10.0.1.11",
		ExpectedMachines: []string{"node-a", "node-b"},
	})

	// The loser is delivered first by wall-clock but claims a later journal
	// sequence, so journal order, not arrival, decides the winner.
	apply(t, p, 5, winner)
	apply(t, p, 9, loser)

	conflicts := p.Conflicts()
	require.Len(t, conflicts, 1)
	require.Equal(t, Key{UnitType: 7, UnitID: 42}, conflicts[0].Key)
	require.Equal(t, winner.ProposalID, conflicts[0].Winner)
	require.Equal(t, []string{loser.ProposalID}, conflicts[0].Losers)
}

func TestProjectionRejectsAcceptanceOfAConflictLoser(t *testing.T) {
	p := NewProjection()
	winner := proposal(42)
	loser := NewProposed(ProposalIdentity{
		UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Other",
		OriginMachine: "node-b", OriginIP: "10.0.1.11",
		ExpectedMachines: []string{"node-a", "node-b"},
	})
	apply(t, p, 1, winner)
	apply(t, p, 2, loser)

	data, err := json.Marshal(NewAccepted(loser))
	require.NoError(t, err)
	err = p.Apply(t.Context(), eventfabric.Delivery{
		Record: events.Record{Meta: events.Meta{
			ID: "accept-loser", Type: TypeAccepted, SchemaVersion: 1,
			Node: events.Node{Machine: "node-b"},
		}, Data: data},
		Sequence: 3,
	})
	require.ErrorContains(t, err, "losing proposal")
}

func TestProjectionLowestSequenceWinsRegardlessOfApplyOrder(t *testing.T) {
	// The reducer is deterministic even if deliveries are folded out of order:
	// the lowest journal sequence claims the key.
	early := proposal(42)
	late := NewProposed(ProposalIdentity{
		UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Worker",
		OriginMachine: "node-b", OriginIP: "10.0.1.11",
		ExpectedMachines: []string{"node-a", "node-b"},
	})

	inOrder := NewProjection()
	apply(t, inOrder, 1, early)
	apply(t, inOrder, 2, late)

	outOfOrder := NewProjection()
	apply(t, outOfOrder, 2, late)
	apply(t, outOfOrder, 1, early)

	first, _ := inOrder.SelectedProposal(early.Key())
	second, _ := outOfOrder.SelectedProposal(early.Key())
	require.Equal(t, first, second)
	require.Equal(t, early.ProposalID, second)
}

func TestProjectionReplayFromEmptyRebuildsTheSameViews(t *testing.T) {
	build := func() *Projection {
		p := NewProjection()
		a := proposal(1)
		b := proposal(2)
		apply(t, p, 1, a)
		apply(t, p, 2, b)
		apply(t, p, 3, NewConfirmed(a.ProposalID, "node-a"))
		apply(t, p, 4, NewConfirmed(a.ProposalID, "node-b"))
		apply(t, p, 5, NewAccepted(a))
		return p
	}

	first := build()
	second := build()

	a := proposal(1)
	firstStatus, _ := first.ProposalStatus(a.ProposalID)
	secondStatus, _ := second.ProposalStatus(a.ProposalID)
	require.Equal(t, firstStatus, secondStatus)
	require.Equal(t, first.Confirmations(a.ProposalID), second.Confirmations(a.ProposalID))
	require.Equal(t, first.Sequence(), second.Sequence())
	require.Equal(t, uint64(5), first.Sequence())
}

func TestProjectionTracksTheHighWaterSequence(t *testing.T) {
	p := NewProjection()
	proposed := proposal(42)

	apply(t, p, 4, proposed)
	require.Equal(t, uint64(4), p.Sequence())

	// A redelivery at the same sequence does not move the high-water mark, and an
	// earlier sequence never lowers it.
	apply(t, p, 4, proposed)
	require.Equal(t, uint64(4), p.Sequence())
}

func TestProjectionStopsOnAnUnsupportedEvent(t *testing.T) {
	p := NewProjection()
	delivery := eventfabric.Delivery{
		Record:   events.Record{Meta: events.Meta{Type: "platform.registration.unknown", SchemaVersion: 1}, Data: json.RawMessage(`{}`)},
		Sequence: 1,
	}
	err := p.Apply(t.Context(), delivery)
	require.ErrorContains(t, err, "unsupported event")
}

func TestProjectionReportsAnUndecodablePayload(t *testing.T) {
	p := NewProjection()
	delivery := eventfabric.Delivery{
		Record:   events.Record{Meta: events.Meta{Type: TypeProposed, SchemaVersion: 1}, Data: json.RawMessage(`{invalid`)},
		Sequence: 1,
	}
	err := p.Apply(t.Context(), delivery)
	require.ErrorContains(t, err, "decode")
}

func TestProjectionIgnoresOtherDomainsAndAdvances(t *testing.T) {
	p := NewProjection()
	delivery := eventfabric.Delivery{
		Record:   events.Record{Meta: events.Meta{Type: eventfabric.TypeReady}},
		Sequence: 4,
	}
	require.NoError(t, p.Apply(t.Context(), delivery))
	require.Equal(t, uint64(4), p.Sequence())
	require.NoError(t, p.WaitApplied(t.Context(), 4))
}

func TestProjectionWaitAppliedStopsOnProjectionFailure(t *testing.T) {
	p := NewProjection()
	waited := make(chan error, 1)
	go func() { waited <- p.WaitApplied(t.Context(), 2) }()

	err := p.Apply(t.Context(), eventfabric.Delivery{
		Record:   events.Record{Meta: events.Meta{Type: TypeProposed, SchemaVersion: 2}},
		Sequence: 1,
	})
	require.ErrorContains(t, err, "unsupported schema version")
	require.ErrorContains(t, <-waited, "unsupported schema version")
}

func TestProjectionQueriesReturnCopies(t *testing.T) {
	p := NewProjection()
	proposed := proposal(42)
	apply(t, p, 1, proposed)

	got, found := p.Proposal(proposed.ProposalID)
	require.True(t, found)
	got.ExpectedMachines[0] = "changed"

	again, found := p.Proposal(proposed.ProposalID)
	require.True(t, found)
	require.Equal(t, []string{"node-a", "node-b"}, again.ExpectedMachines)
}

func TestProjectionUnknownProposalHasNoStatus(t *testing.T) {
	p := NewProjection()
	_, ok := p.ProposalStatus("missing")
	require.False(t, ok)
	require.False(t, p.IsAccepted("missing"))
	require.False(t, p.AllExpectedConfirmed("missing"))
}
