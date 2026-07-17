package eventmodel

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/internal/eventfabric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

type publishedEvent struct {
	event         events.Event
	causationID   string
	correlationID string
}

type recordingPublisher struct {
	published []publishedEvent
	err       error
}

func (p *recordingPublisher) Publish(ctx context.Context, event events.Event) (eventfabric.Receipt, error) {
	if p.err != nil {
		return eventfabric.Receipt{}, p.err
	}
	cause, correlation := eventfabric.CausalLinks(ctx)
	p.published = append(p.published, publishedEvent{event: event, causationID: cause, correlationID: correlation})
	return eventfabric.Receipt{ID: "published", Sequence: uint64(len(p.published))}, nil
}

func locations() []Location {
	return []Location{{Machine: "node-b", IP: "10.0.1.11"}, {Machine: "node-a", IP: "10.0.1.10"}}
}

func TestCommandServicePublishesADurableProposal(t *testing.T) {
	publisher := &recordingPublisher{}
	commands, _, err := Open(publisher, NewProjection(), locations()[1], locations())
	require.NoError(t, err)

	role := api.RoleMaster
	result, err := commands.Create(t.Context(), api.RegistrationRequest{
		UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Billing", Role: &role,
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.ProposalID)
	require.Equal(t, uint64(1), result.Sequence)
	require.Len(t, publisher.published, 1)

	proposed, ok := publisher.published[0].event.(Proposed)
	require.True(t, ok)
	require.Equal(t, result.ProposalID, proposed.ProposalID)
	require.Equal(t, []string{"node-a", "node-b"}, proposed.ExpectedMachines)
	require.Equal(t, "node-a", proposed.OriginMachine)
	require.Empty(t, publisher.published[0].causationID)
}

func TestCommandServiceRejectsInvalidInputBeforePublishing(t *testing.T) {
	publisher := &recordingPublisher{}
	commands, _, err := Open(publisher, NewProjection(), locations()[1], locations())
	require.NoError(t, err)

	_, err = commands.Create(t.Context(), api.RegistrationRequest{UnitTypeNameAdvertised: " "})
	require.ErrorContains(t, err, "blank")
	require.Empty(t, publisher.published)
}

func TestCommandServiceClassifiesPublishFailure(t *testing.T) {
	publisher := &recordingPublisher{err: errors.New("connection lost")}
	commands, _, err := Open(publisher, NewProjection(), locations()[1], locations())
	require.NoError(t, err)

	_, err = commands.Create(t.Context(), api.RegistrationRequest{UnitTypeNameAdvertised: "Worker"})
	require.ErrorIs(t, err, ErrJournalUnavailable)
	require.ErrorContains(t, err, "connection lost")
}

func TestQueryServiceReadsOnlyTheLocalProjection(t *testing.T) {
	projection := NewProjection()
	publisher := &recordingPublisher{}
	_, queries, err := Open(publisher, projection, locations()[1], locations())
	require.NoError(t, err)
	proposed := proposal(42)
	apply(t, projection, 1, proposed)
	apply(t, projection, 2, NewConfirmed(proposed.ProposalID, "node-a"))

	view, found := queries.Get(proposed.ProposalID)
	require.True(t, found)
	require.Equal(t, api.RegistrationStatusPending, view.Status)
	require.Equal(t, api.RegistrationStatusAccepted, view.PlatformInstances[0].Status)
	require.Equal(t, api.RegistrationStatusPending, view.PlatformInstances[1].Status)
	require.Empty(t, publisher.published, "queries do not publish or call the Event Fabric")
}

func TestQueryServiceReportsProjectedConflicts(t *testing.T) {
	projection := NewProjection()
	_, queries, err := Open(&recordingPublisher{}, projection, locations()[1], locations())
	require.NoError(t, err)
	winner := proposal(42)
	loser := NewProposed(ProposalIdentity{
		UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Other",
		OriginMachine: "node-b", OriginIP: "10.0.1.11",
		ExpectedMachines: []string{"node-a", "node-b"},
	})
	apply(t, projection, 1, winner)
	apply(t, projection, 2, loser)

	conflicts, err := queries.Conflicts()
	require.NoError(t, err)
	require.Len(t, conflicts, 1)
	require.Equal(t, "node-a", conflicts[0].Winner.Machine)
	require.Equal(t, api.RegistrationStatusRejected, conflicts[0].Losers[0].Status)
	require.Equal(t, ReasonKeyConflict, *conflicts[0].Losers[0].Reason)
}

func TestQueryServiceListsProposalsInJournalOrder(t *testing.T) {
	projection := NewProjection()
	_, queries, err := Open(&recordingPublisher{}, projection, locations()[1], locations())
	require.NoError(t, err)
	first := proposal(2)
	second := proposal(1)
	apply(t, projection, 4, first)
	apply(t, projection, 9, second)

	listed := queries.List()
	require.Len(t, listed, 2)
	require.Equal(t, uint16(2), listed[0].UnitID)
	require.Equal(t, uint16(1), listed[1].UnitID)
	sequence, found := projection.ProposalSequence(second.ProposalID)
	require.True(t, found)
	require.Equal(t, uint64(9), sequence)
}

func TestOpenValidatesTrustedTopology(t *testing.T) {
	publisher := &recordingPublisher{}
	_, _, err := Open(nil, NewProjection(), locations()[1], locations())
	require.ErrorContains(t, err, "publisher")
	_, _, err = Open(publisher, nil, locations()[1], locations())
	require.ErrorContains(t, err, "projection")
	_, _, err = Open(publisher, NewProjection(), Location{Machine: "other", IP: "10.0.1.12"}, locations())
	require.ErrorContains(t, err, "not in the expected")
}
