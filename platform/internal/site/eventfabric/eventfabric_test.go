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
)

// There is no implementation to test here yet. What these tests establish is
// that the contract is the one its consumer needs: a stub Consumer drives a
// minimal fold through a run of deliveries, and a restart of that consumer
// resumes from its own acknowledged position. If the step 06 ADR amends
// Consumer, this is what has to keep working.

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

// typeFixtureNoted is a minimal site-scoped fact used only to drive these
// contract tests; it carries nothing a real projection would need.
const typeFixtureNoted events.Type = "platform.fixture.noted"

type fixtureNoted struct {
	N int `json:"n"`
}

func (fixtureNoted) EventType() events.Type { return typeFixtureNoted }

// fold reads want deliveries as the named consumer, keeping the highest
// sequence applied and the order deliveries arrived in, which is the loop a
// durable reader runs.
func fold(t *testing.T, fabric eventfabric.Consumer, name string, want int) []uint64 {
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
			require.NoError(t, fabric.Ack(ctx, name, result.Delivery.Sequence))
			sequences = append(sequences, result.Delivery.Sequence)
		case <-time.After(5 * time.Second):
			require.FailNowf(t, "stream went quiet", "got %d of %d deliveries", len(sequences), want)
		}
	}
	return sequences
}

func TestConsumerFeedsAFold(t *testing.T) {
	t.Parallel()

	fabric := newStub(
		delivery(t, 1, fixtureNoted{N: 1}, "node-a"),
		delivery(t, 2, fixtureNoted{N: 2}, "node-a"),
		delivery(t, 3, fixtureNoted{N: 3}, "node-b"),
	)

	sequences := fold(t, fabric, "consumer", 3)
	require.Equal(t, []uint64{1, 2, 3}, sequences,
		"a fold sees deliveries in the site order the fabric assigned")
}

func TestConsumerResumesFromItsOwnAcknowledgedPosition(t *testing.T) {
	t.Parallel()

	fabric := newStub(
		delivery(t, 1, fixtureNoted{N: 1}, "node-a"),
		delivery(t, 2, fixtureNoted{N: 2}, "node-a"),
		delivery(t, 3, fixtureNoted{N: 3}, "node-b"),
	)

	// One consumer stops after the first delivery, then follows again. The
	// position is the named consumer's, so the second follow starts where the
	// first stopped rather than at the head of the stream.
	fold(t, fabric, "consumer", 1)

	require.Equal(t, []uint64{2, 3}, fold(t, fabric, "consumer", 2),
		"a restarted consumer is given what it had not acknowledged, and not what it had")

	require.Equal(t, []uint64{1, 2, 3}, fold(t, fabric, "projector", 3),
		"a second consumer name starts from the beginning, unaffected by the first")
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
