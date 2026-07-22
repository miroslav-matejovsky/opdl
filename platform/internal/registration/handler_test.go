package registration

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/eventfabric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

func delivery(t *testing.T, sequence uint64, event events.Event, machine, id string) eventfabric.Delivery {
	t.Helper()
	return eventfabric.Delivery{Envelope: testEnvelope(t, event, machine, id), Sequence: sequence}
}

func applyDelivery(t *testing.T, projection *Projection, delivery eventfabric.Delivery) {
	t.Helper()
	require.NoError(t, projection.Apply(t.Context(), delivery))
}

// handle runs one delivery through handler the way the Event Fabric does, with
// the delivery attached to the context as the cause of whatever the handler
// publishes. The handler itself knows nothing about that.
func handle(t *testing.T, handler *Handler, delivery eventfabric.Delivery) error {
	t.Helper()
	return handler.Handle(events.WithCause(t.Context(), delivery.Envelope), delivery)
}

func TestHandlerConfirmsAValidClaimingProposal(t *testing.T) {
	projection := NewProjection()
	publisher, appender := testPublisher(t)
	handler, err := NewHandler(publisher, projection, locations()[1], locations(), eventfabric.NewSiteScope("p", "e", "s"))
	require.NoError(t, err)
	proposed := proposal(42)
	input := delivery(t, 1, proposed, "node-a", "proposal-event")
	applyDelivery(t, projection, input)

	require.NoError(t, handle(t, handler, input))
	require.Len(t, appender.appended, 1)
	confirmed := payload[Confirmed](t, appender, 0)
	require.Equal(t, proposed.ProposalID, confirmed.ProposalID)
	require.Equal(t, "node-a", confirmed.DecidingMachine)
	require.Equal(t, "proposal-event", appender.appended[0].CausationID)
	require.Equal(t, "proposal-event", appender.appended[0].CorrelationID)
}

func TestHandlerRejectsAConflictingProposal(t *testing.T) {
	projection := NewProjection()
	publisher, appender := testPublisher(t)
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

	require.NoError(t, handle(t, handler, input))
	rejected := payload[Rejected](t, appender, 0)
	require.Equal(t, ReasonKeyConflict, rejected.Reason)
}

func TestHandlerRejectsASemanticallyInvalidProposal(t *testing.T) {
	projection := NewProjection()
	publisher, appender := testPublisher(t)
	handler, err := NewHandler(publisher, projection, locations()[1], locations(), eventfabric.NewSiteScope("p", "e", "s"))
	require.NoError(t, err)
	invalid := NewProposed(ProposalIdentity{
		UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: " ",
		OriginMachine: "node-a", OriginIP: "10.0.1.10",
		ExpectedMachines: []string{"node-a", "node-b"},
	})
	input := delivery(t, 1, invalid, "node-a", "invalid")
	applyDelivery(t, projection, input)

	require.NoError(t, handle(t, handler, input))
	rejected := payload[Rejected](t, appender, 0)
	require.Equal(t, ReasonInvalidProposal, rejected.Reason)
}

func TestOriginHandlerAcceptsOnlyAfterEveryConfirmation(t *testing.T) {
	projection := NewProjection()
	publisher, appender := testPublisher(t)
	handler, err := NewHandler(publisher, projection, locations()[1], locations(), eventfabric.NewSiteScope("p", "e", "s"))
	require.NoError(t, err)
	proposed := proposal(42)
	applyDelivery(t, projection, delivery(t, 1, proposed, "node-a", "proposal"))

	first := delivery(t, 2, NewConfirmed(proposed.ProposalID, "node-a"), "node-a", "confirm-a")
	applyDelivery(t, projection, first)
	require.NoError(t, handle(t, handler, first))
	require.Empty(t, appender.appended)

	second := delivery(t, 3, NewConfirmed(proposed.ProposalID, "node-b"), "node-b", "confirm-b")
	applyDelivery(t, projection, second)
	require.NoError(t, handle(t, handler, second))
	require.Len(t, appender.appended, 1)
	accepted := payload[Accepted](t, appender, 0)
	require.Equal(t, proposed.ProposalID, accepted.ProposalID)
	require.Equal(t, "confirm-b", appender.appended[0].CausationID)
	require.Equal(t, "confirm-b", appender.appended[0].CorrelationID)
}

func TestHandlerDeclaresOnlyFiniteRegistrationRoutes(t *testing.T) {
	handler, err := NewHandler(anyPublisher(t), NewProjection(), locations()[1], locations(), eventfabric.NewSiteScope("p", "e", "s"))
	require.NoError(t, err)
	require.Equal(t, "registration", handler.Name())
	routes := handler.Routes()
	require.Len(t, routes, 2)
	require.Contains(t, routes[0].Subject(), "registration.proposed")
	require.Contains(t, routes[1].Subject(), "registration.confirmed")
}
