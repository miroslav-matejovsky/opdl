package registration

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/eventfabric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

func delivery(t *testing.T, sequence uint64, event events.Event, machine, id string) eventfabric.Delivery {
	t.Helper()
	data, err := json.Marshal(event)
	require.NoError(t, err)
	return eventfabric.Delivery{
		Record: events.Record{Meta: events.Meta{
			ID: id, Type: event.EventType(), SchemaVersion: 1,
			Node: events.Node{Machine: machine},
		}, Data: data},
		Sequence: sequence,
	}
}

func applyDelivery(t *testing.T, projection *Projection, delivery eventfabric.Delivery) {
	t.Helper()
	require.NoError(t, projection.Apply(t.Context(), delivery))
}

func TestHandlerConfirmsAValidClaimingProposal(t *testing.T) {
	projection := NewProjection()
	publisher := &recordingPublisher{}
	handler, err := NewHandler(publisher, projection, locations()[1], locations(), eventfabric.NewSiteScope("p", "e", "s"))
	require.NoError(t, err)
	proposed := proposal(42)
	input := delivery(t, 1, proposed, "node-a", "proposal-event")
	applyDelivery(t, projection, input)

	require.NoError(t, handler.Handle(t.Context(), input))
	require.Len(t, publisher.published, 1)
	confirmed, ok := publisher.published[0].event.(Confirmed)
	require.True(t, ok)
	require.Equal(t, proposed.ProposalID, confirmed.ProposalID)
	require.Equal(t, "node-a", confirmed.DecidingMachine)
	require.Equal(t, "proposal-event", publisher.published[0].causationID)
	require.Equal(t, "proposal-event", publisher.published[0].correlationID)
}

func TestHandlerRejectsAConflictingProposal(t *testing.T) {
	projection := NewProjection()
	publisher := &recordingPublisher{}
	handler, err := NewHandler(publisher, projection, locations()[1], locations(), eventfabric.NewSiteScope("p", "e", "s"))
	require.NoError(t, err)
	winner := proposal(42)
	loser := NewProposed(ProposalIdentity{
		UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Other",
		OriginMachine: "node-b", OriginIP: "10.0.1.11",
		ExpectedMachines: []string{"node-a", "node-b"},
	})
	applyDelivery(t, projection, delivery(t, 1, winner, "node-a", "winner"))
	input := delivery(t, 2, loser, "node-b", "loser")
	applyDelivery(t, projection, input)

	require.NoError(t, handler.Handle(t.Context(), input))
	rejected, ok := publisher.published[0].event.(Rejected)
	require.True(t, ok)
	require.Equal(t, ReasonKeyConflict, rejected.Reason)
}

func TestHandlerRejectsASemanticallyInvalidProposal(t *testing.T) {
	projection := NewProjection()
	publisher := &recordingPublisher{}
	handler, err := NewHandler(publisher, projection, locations()[1], locations(), eventfabric.NewSiteScope("p", "e", "s"))
	require.NoError(t, err)
	invalid := NewProposed(ProposalIdentity{
		UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: " ",
		OriginMachine: "node-a", OriginIP: "10.0.1.10",
		ExpectedMachines: []string{"node-a", "node-b"},
	})
	input := delivery(t, 1, invalid, "node-a", "invalid")
	applyDelivery(t, projection, input)

	require.NoError(t, handler.Handle(t.Context(), input))
	rejected, ok := publisher.published[0].event.(Rejected)
	require.True(t, ok)
	require.Equal(t, ReasonInvalidProposal, rejected.Reason)
}

func TestOriginHandlerAcceptsOnlyAfterEveryConfirmation(t *testing.T) {
	projection := NewProjection()
	publisher := &recordingPublisher{}
	handler, err := NewHandler(publisher, projection, locations()[1], locations(), eventfabric.NewSiteScope("p", "e", "s"))
	require.NoError(t, err)
	proposed := proposal(42)
	applyDelivery(t, projection, delivery(t, 1, proposed, "node-a", "proposal"))

	first := delivery(t, 2, NewConfirmed(proposed.ProposalID, "node-a"), "node-a", "confirm-a")
	applyDelivery(t, projection, first)
	require.NoError(t, handler.Handle(t.Context(), first))
	require.Empty(t, publisher.published)

	second := delivery(t, 3, NewConfirmed(proposed.ProposalID, "node-b"), "node-b", "confirm-b")
	applyDelivery(t, projection, second)
	require.NoError(t, handler.Handle(t.Context(), second))
	require.Len(t, publisher.published, 1)
	accepted, ok := publisher.published[0].event.(Accepted)
	require.True(t, ok)
	require.Equal(t, proposed.ProposalID, accepted.ProposalID)
	require.Equal(t, "confirm-b", publisher.published[0].causationID)
	require.Equal(t, "confirm-b", publisher.published[0].correlationID)
}

func TestHandlerDeclaresOnlyFiniteRegistrationRoutes(t *testing.T) {
	handler, err := NewHandler(&recordingPublisher{}, NewProjection(), locations()[1], locations(), eventfabric.NewSiteScope("p", "e", "s"))
	require.NoError(t, err)
	require.Equal(t, "registration", handler.Name())
	routes := handler.Routes()
	require.Len(t, routes, 2)
	require.Contains(t, routes[0].Subject(), "registration.proposed")
	require.Contains(t, routes[1].Subject(), "registration.confirmed")
}
