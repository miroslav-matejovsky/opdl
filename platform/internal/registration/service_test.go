package registration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage"
)

// testDescriptor is the deployment identity the tests' envelope factory stamps.
var testDescriptor = config.Descriptor{
	Platform: "opdl", Project: "customer-a", Environment: "production",
	Site: "north", Machine: "node-a", MachineProfile: "all-in-one", IP: "10.0.1.10",
}

// recordingBackend is the storage backend: it keeps the completed
// envelopes a publisher hands it, which is exactly what a real backend receives.
type recordingBackend struct {
	stored []events.Envelope
	err    error
}

func (b *recordingBackend) Store(_ context.Context, envelope events.Envelope) error {
	if b.err != nil {
		return b.err
	}
	b.stored = append(b.stored, envelope)
	return nil
}

func (b *recordingBackend) Close(_ context.Context) error {
	return nil
}

// testPublisher composes the real publisher over a recording backend.
func testPublisher(t *testing.T) (events.Publisher, *recordingBackend) {
	t.Helper()
	factory, err := events.NewFactory(testDescriptor, "primary")
	require.NoError(t, err)
	backend := &recordingBackend{}
	pub, err := storage.NewPublisher(factory, backend)
	require.NoError(t, err)
	return pub, backend
}

// anyPublisher composes a working publisher for a test that never reads what
// was published.
func anyPublisher(t *testing.T) events.Publisher {
	t.Helper()
	publisher, _ := testPublisher(t)
	return publisher
}

func locations() []Location {
	return []Location{{Machine: "node-b", IP: "10.0.1.11"}, {Machine: "node-a", IP: "10.0.1.10"}}
}

func TestCommandServiceReturnsNotImplemented(t *testing.T) {
	publisher, _ := testPublisher(t)
	commands, _, err := Open(publisher, NewProjection(), locations()[1], locations())
	require.NoError(t, err)

	role := api.RoleMaster
	_, err = commands.Create(t.Context(), api.RegistrationRequest{
		UnitType: 7, UnitID: 42, UnitTypeNameAdvertised: "Billing", Role: &role,
	})
	require.ErrorIs(t, err, api.ErrNotImplemented)
}

func TestQueryServiceReadsOnlyTheLocalProjection(t *testing.T) {
	projection := NewProjection()
	publisher, backend := testPublisher(t)
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
	require.Empty(t, backend.stored, "queries do not publish or call the Event Fabric")
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
