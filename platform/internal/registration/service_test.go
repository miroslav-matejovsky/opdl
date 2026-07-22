package registration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/eventfabric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// testDescriptor is the deployment identity the tests' envelope factory stamps.
var testDescriptor = config.Descriptor{
	Platform: "opdl", Project: "customer-a", Environment: "production",
	Site: "north", Machine: "node-a", MachineProfile: "all-in-one", IP: "10.0.1.10",
}

// recordingAppender is the journal half of publication: it keeps the completed
// envelopes a publisher hands it, which is exactly what a real journal receives.
// Registration never sees it; it is given the publisher composed over it.
type recordingAppender struct {
	appended []events.Envelope
	err      error
}

func (a *recordingAppender) Append(_ context.Context, envelope events.Envelope) (eventfabric.Receipt, error) {
	if a.err != nil {
		return eventfabric.Receipt{}, a.err
	}
	a.appended = append(a.appended, envelope)
	return eventfabric.Receipt{ID: envelope.ID, Sequence: uint64(len(a.appended))}, nil
}

// testPublisher composes the real publisher over a recording appender, so a test
// exercises the same stamping the runtime does and can then read what the
// journal would have stored.
func testPublisher(t *testing.T) (eventfabric.Publisher, *recordingAppender) {
	t.Helper()
	factory, err := events.NewFactory(testDescriptor, "primary")
	require.NoError(t, err)
	appender := &recordingAppender{}
	return eventfabric.NewPublisher(factory, appender), appender
}

// anyPublisher composes a working publisher for a test that never reads what
// was published.
func anyPublisher(t *testing.T) eventfabric.Publisher {
	t.Helper()
	publisher, _ := testPublisher(t)
	return publisher
}

// failingPublisher composes a publisher whose journal always refuses.
func failingPublisher(t *testing.T, cause error) eventfabric.Publisher {
	t.Helper()
	factory, err := events.NewFactory(testDescriptor, "primary")
	require.NoError(t, err)
	return eventfabric.NewPublisher(factory, &recordingAppender{err: cause})
}

// payload decodes the payload of the index-th appended envelope, so a test reads
// the fact the journal would have stored rather than the struct it passed in.
func payload[T events.Event](t *testing.T, appender *recordingAppender, index int) T {
	t.Helper()
	require.Greater(t, len(appender.appended), index, "no event was published at index %d", index)
	var event T
	require.NoError(t, json.Unmarshal(appender.appended[index].Data, &event))
	require.Equal(t, event.EventType(), appender.appended[index].Type)
	return event
}

func locations() []Location {
	return []Location{{Machine: "node-b", IP: "10.0.1.11"}, {Machine: "node-a", IP: "10.0.1.10"}}
}

func TestCommandServicePublishesADurableProposal(t *testing.T) {
	publisher, appender := testPublisher(t)
	commands, _, err := Open(publisher, NewProjection(), locations()[1], locations())
	require.NoError(t, err)

	role := api.RoleMaster
	result, err := commands.Create(t.Context(), api.RegistrationRequest{
		UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Billing", Role: &role,
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.ProposalID)
	require.Equal(t, uint64(1), result.Sequence)
	require.Len(t, appender.appended, 1)

	proposed := payload[Proposed](t, appender, 0)
	require.Equal(t, result.ProposalID, proposed.ProposalID)
	require.Equal(t, []string{"node-a", "node-b"}, proposed.ExpectedMachines)
	require.Equal(t, "node-a", proposed.OriginMachine)

	stamped := appender.appended[0]
	require.NoError(t, stamped.Validate(), "the command service publishes a journal-valid envelope")
	require.Equal(t, "node-a", stamped.Origin.Machine, "the origin is the process's, never the caller's")
	require.Empty(t, stamped.CausationID, "a command begins a chain, so it has no cause")
	require.Empty(t, stamped.CorrelationID)
}

func TestCommandServiceRejectsInvalidInputBeforePublishing(t *testing.T) {
	publisher, appender := testPublisher(t)
	commands, _, err := Open(publisher, NewProjection(), locations()[1], locations())
	require.NoError(t, err)

	_, err = commands.Create(t.Context(), api.RegistrationRequest{UnitTypeNameAdvertised: " "})
	require.ErrorContains(t, err, "blank")
	require.Empty(t, appender.appended)
}

func TestCommandServiceClassifiesPublishFailure(t *testing.T) {
	publisher := failingPublisher(t, errors.New("connection lost"))
	commands, _, err := Open(publisher, NewProjection(), locations()[1], locations())
	require.NoError(t, err)

	_, err = commands.Create(t.Context(), api.RegistrationRequest{UnitTypeNameAdvertised: "Worker"})
	require.ErrorIs(t, err, api.ErrJournalUnavailable)
	require.ErrorContains(t, err, "connection lost")
}

func TestQueryServiceReadsOnlyTheLocalProjection(t *testing.T) {
	projection := NewProjection()
	publisher, appender := testPublisher(t)
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
	require.Empty(t, appender.appended, "queries do not publish or call the Event Fabric")
}

func TestQueryServiceReportsProjectedConflicts(t *testing.T) {
	projection := NewProjection()
	_, queries, err := Open(anyPublisher(t), projection, locations()[1], locations())
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
	_, queries, err := Open(anyPublisher(t), projection, locations()[1], locations())
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
	publisher := anyPublisher(t)
	_, _, err := Open(nil, NewProjection(), locations()[1], locations())
	require.ErrorContains(t, err, "publisher")
	_, _, err = Open(publisher, nil, locations()[1], locations())
	require.ErrorContains(t, err, "projection")
	_, _, err = Open(publisher, NewProjection(), Location{Machine: "other", IP: "10.0.1.12"}, locations())
	require.ErrorContains(t, err, "not in the expected")
}
