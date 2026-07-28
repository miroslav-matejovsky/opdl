package eventfabric_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/site/eventfabric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/site/registration"
)

// There is no implementation to test here yet. What these tests establish is
// that the contract is the one its consumer needs: a stub Consumer drives
// registration.Projection through a full proposal, and a restart of that
// consumer resumes from its own acknowledged position. If the step 06 ADR
// amends Consumer, this is what has to keep working.

// stub is a Consumer over a fixed list of deliveries, keeping a durable
// position per consumer name the way an implementation would.
type stub struct {
	deliveries []eventfabric.Delivery
	acked      map[string]uint64
}

func newStub(deliveries ...eventfabric.Delivery) *stub {
	return &stub{deliveries: deliveries, acked: make(map[string]uint64)}
}

// Follow replays what name has not acknowledged and then waits for more, which
// for a fixed list means waiting for ctx.
func (s *stub) Follow(ctx context.Context, name string) (<-chan eventfabric.Result, error) {
	from := s.acked[name]
	results := make(chan eventfabric.Result)
	go func() {
		defer close(results)
		for _, delivery := range s.deliveries {
			if delivery.Sequence <= from {
				continue
			}
			select {
			case results <- eventfabric.Result{Delivery: delivery}:
			case <-ctx.Done():
				return
			}
		}
		<-ctx.Done()
	}()
	return results, nil
}

func (s *stub) Ack(_ context.Context, name string, sequence uint64) error {
	if sequence > s.acked[name] {
		s.acked[name] = sequence
	}
	return nil
}

var _ eventfabric.Consumer = (*stub)(nil)

func TestConsumerFeedsARegistrationProjection(t *testing.T) {
	t.Parallel()

	proposal := testProposal()
	fabric := newStub(
		delivery(t, 1, proposal, proposal.OriginMachine),
		delivery(t, 2, registration.NewConfirmed(proposal.ProposalID, "node-a"), "node-a"),
		delivery(t, 3, registration.NewConfirmed(proposal.ProposalID, "node-b"), "node-b"),
	)

	projection := registration.NewProjection()
	fold(t, fabric, projection, "registration", 3)

	require.Equal(t, uint64(3), projection.Sequence(),
		"the projection's position is the site order the fabric delivered")
	require.True(t, projection.AllExpectedConfirmed(proposal.ProposalID),
		"a projection folded from the contract answers as it does from any journal")
}

func TestConsumerResumesFromItsOwnAcknowledgedPosition(t *testing.T) {
	t.Parallel()

	proposal := testProposal()
	fabric := newStub(
		delivery(t, 1, proposal, proposal.OriginMachine),
		delivery(t, 2, registration.NewConfirmed(proposal.ProposalID, "node-a"), "node-a"),
		delivery(t, 3, registration.NewConfirmed(proposal.ProposalID, "node-b"), "node-b"),
	)

	// One consumer stops after the proposal, then follows again. The position
	// is the named consumer's, so the second follow starts where the first
	// stopped rather than at the head of the stream.
	projection := registration.NewProjection()
	fold(t, fabric, projection, "registration", 1)

	require.Equal(t, []uint64{2, 3}, fold(t, fabric, projection, "registration", 2),
		"a restarted consumer is given what it had not acknowledged, and not what it had")

	other := registration.NewProjection()
	require.Equal(t, []uint64{1, 2, 3}, fold(t, fabric, other, "projector", 3),
		"a second consumer name starts from the beginning, unaffected by the first")
}

// fold reads want deliveries as the named consumer, applies each to projection,
// and acknowledges it, which is the loop a durable reader runs.
func fold(t *testing.T, fabric eventfabric.Consumer, projection *registration.Projection, name string, want int) []uint64 {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	results, err := fabric.Follow(ctx, name)
	require.NoError(t, err)

	sequences := make([]uint64, 0, want)
	for len(sequences) < want {
		select {
		case result, ok := <-results:
			require.Truef(t, ok, "stream closed after %d of %d deliveries", len(sequences), want)
			require.NoError(t, result.Err)
			require.NoError(t, projection.Apply(ctx, result.Delivery))
			require.NoError(t, fabric.Ack(ctx, name, result.Delivery.Sequence))
			sequences = append(sequences, result.Delivery.Sequence)
		case <-time.After(5 * time.Second):
			require.FailNowf(t, "stream went quiet", "got %d of %d deliveries", len(sequences), want)
		}
	}
	return sequences
}

// testProposal is one proposal for a two-machine site.
func testProposal() registration.Proposed {
	return registration.NewProposed(registration.ProposalIdentity{
		UnitType:               7,
		UnitID:                 42,
		UnitTypeNameAdvertised: "Worker",
		OriginMachine:          "node-a",
		OriginIP:               "10.0.1.10",
		ExpectedMachines:       []string{"node-a", "node-b"},
	})
}

// delivery stamps the envelope the fabric would have carried for event, as
// stated by machine, and puts it at one site sequence.
func delivery(t *testing.T, sequence uint64, event events.Event, machine string) eventfabric.Delivery {
	t.Helper()
	data, err := json.Marshal(event)
	require.NoError(t, err)

	envelope := events.Envelope{
		ID:            fmt.Sprintf("id-%d", sequence),
		Type:          event.EventType(),
		SchemaVersion: events.DefaultSchemaVersion,
		OccurredAt:    time.Date(2026, time.July, 28, 9, 0, 0, 0, time.UTC),
		Source:        event.EventType().Source(),
		Severity:      events.DefaultSeverity,
		Scope:         events.ScopeSite,
		Origin: events.Origin{
			Machine:        machine,
			MachineProfile: "all-in-one",
			ProcessRole:    "primary",
			PID:            4242,
		},
		Data: data,
	}
	require.NoError(t, envelope.Validate())
	return eventfabric.Delivery{Envelope: envelope, Sequence: sequence}
}
