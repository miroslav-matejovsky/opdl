package redundancy_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage"
	"github.com/miroslav-matejovsky/opdl/platform/internal/redundancy"
)

func TestRedundancyEventsDeclareTheirContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		event    events.Event
		wantType events.Type
		want     events.Severity
		wantJSON string
	}{
		{
			name:     "lease opened",
			event:    redundancy.LeaseOpened{File: "D:/opdl/lease"},
			wantType: redundancy.TypeLeaseOpened,
			want:     events.SeverityInfo,
			wantJSON: `{"file":"D:/opdl/lease"}`,
		},
		{
			name:     "ownership waiting",
			event:    redundancy.OwnershipWaiting{File: "D:/opdl/lease"},
			wantType: redundancy.TypeOwnershipWaiting,
			want:     events.SeverityInfo,
			wantJSON: `{"file":"D:/opdl/lease"}`,
		},
		{
			name:     "ownership handed over",
			event:    redundancy.OwnershipAcquired{File: "D:/opdl/lease"},
			wantType: redundancy.TypeOwnershipAcquired,
			want:     events.SeverityInfo,
			wantJSON: `{"file":"D:/opdl/lease","abandoned":false}`,
		},
		{
			name:     "ownership taken from a lapsed lease",
			event:    redundancy.OwnershipAcquired{File: "D:/opdl/lease", Abandoned: true},
			wantType: redundancy.TypeOwnershipAcquired,
			want:     events.SeverityWarn,
			wantJSON: `{"file":"D:/opdl/lease","abandoned":true}`,
		},
		{
			name:     "promotion declined",
			event:    redundancy.PromotionDeclined{Reason: "peer is healthy"},
			wantType: redundancy.TypePromotionDeclined,
			want:     events.SeverityInfo,
			wantJSON: `{"reason":"peer is healthy"}`,
		},
		{
			name:     "lease renewal failed",
			event:    redundancy.LeaseRenewalFailed{Error: "disk stalled"},
			wantType: redundancy.TypeLeaseRenewalFailed,
			want:     events.SeverityWarn,
			wantJSON: `{"error":"disk stalled"}`,
		},
		{
			name:     "stepped down",
			event:    redundancy.SteppedDown{Reason: "ownership was taken over"},
			wantType: redundancy.TypeSteppedDown,
			want:     events.SeverityWarn,
			wantJSON: `{"reason":"ownership was taken over"}`,
		},
		{
			name:     "failback initiated",
			event:    redundancy.FailbackInitiated{},
			wantType: redundancy.TypeFailbackInitiated,
			want:     events.SeverityInfo,
			wantJSON: `{}`,
		},
		{
			name:     "activation started",
			event:    redundancy.ActivationStarted{Kind: redundancy.ActivationFailover},
			wantType: redundancy.TypeActivationStarted,
			want:     events.SeverityInfo,
			wantJSON: `{"activation_kind":"failover"}`,
		},
		{
			name:     "activation failed",
			event:    redundancy.ActivationFailed{Kind: redundancy.ActivationFailback, DurationMS: 1200, Error: "journal unreachable"},
			wantType: redundancy.TypeActivationFailed,
			want:     events.SeverityError,
			wantJSON: `{"activation_kind":"failback","duration_ms":1200,"error":"journal unreachable"}`,
		},
		{
			name:     "activation completed",
			event:    redundancy.ActivationCompleted{Kind: redundancy.ActivationInitial, DurationMS: 90},
			wantType: redundancy.TypeActivationCompleted,
			want:     events.SeverityInfo,
			wantJSON: `{"activation_kind":"initial activation","duration_ms":90}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, test.wantType, test.event.EventType())
			require.NoError(t, test.event.EventType().Validate())
			require.Equal(t, "redundancy", test.event.EventType().Source(),
				"the source is derived from the type, so no event restates it")

			severity := events.SeverityInfo
			if severe, ok := test.event.(events.Severe); ok {
				severity = severe.Severity()
			}
			require.Equal(t, test.want, severity)

			data, err := json.Marshal(test.event)
			require.NoError(t, err)
			require.JSONEq(t, test.wantJSON, string(data))
		})
	}
}

// TestContendRecordsTheOwnershipLifecycle runs a real ownership lifecycle
// through a real fan-out publisher over a capturing backend, so what an operator
// would read is what is asserted: the typed events, in order, as canonical
// envelopes.
func TestContendRecordsTheOwnershipLifecycle(t *testing.T) {
	t.Parallel()

	lease := openLease(t, leaseConfig(t), redundancy.RolePrimary)
	runtime := newRecordingRuntime()
	publisher, recorded := recording(t)
	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)
	go func() { done <- redundancy.Contend(ctx, publisher, lease, unhealthyPeer, runtime.runtime()) }()
	require.Equal(t, redundancy.ActivationInitial, <-runtime.activeKinds)
	cancel()
	require.NoError(t, <-done)

	envelopes := recorded()
	require.Equal(t, []events.Type{
		redundancy.TypeLeaseOpened,
		redundancy.TypeOwnershipAcquired,
		redundancy.TypeActivationStarted,
		redundancy.TypeActivationCompleted,
	}, types(envelopes), "an uncontested start never waits, so it never says it did")

	acquired := envelopes[1]
	require.Equal(t, events.SeverityInfo, acquired.Severity, "nobody died; this was a free lease")
	require.Equal(t, "primary", acquired.Origin.ProcessRole, "which process took ownership is in the origin")
	var payload redundancy.OwnershipAcquired
	require.NoError(t, json.Unmarshal(acquired.Data, &payload))
	require.False(t, payload.Abandoned)
	require.Equal(t, lease.File(), payload.File)

	var completed redundancy.ActivationCompleted
	require.NoError(t, json.Unmarshal(envelopes[3].Data, &completed))
	require.Equal(t, redundancy.ActivationInitial, completed.Kind)
	require.GreaterOrEqual(t, completed.DurationMS, int64(0), "an activation reports how long it held ownership")
}

// TestContendRecordsAFailedActivation checks the failure path keeps what an
// operator needs: the original error and how long the instance lasted.
func TestContendRecordsAFailedActivation(t *testing.T) {
	t.Parallel()

	notServing := errors.New("fabric would not open")
	lease := openLease(t, leaseConfig(t), redundancy.RolePrimary)
	runtime := newRecordingRuntime()
	runtime.activeErr = notServing
	publisher, recorded := recording(t)

	require.ErrorIs(t, redundancy.Contend(t.Context(), publisher, lease, unhealthyPeer, runtime.runtime()), notServing)

	envelopes := recorded()
	require.Contains(t, types(envelopes), redundancy.TypeActivationFailed)
	require.NotContains(t, types(envelopes), redundancy.TypeActivationCompleted,
		"an activation that failed did not complete")

	failed := envelopes[len(envelopes)-1]
	require.Equal(t, redundancy.TypeActivationFailed, failed.Type)
	require.Equal(t, events.SeverityError, failed.Severity)
	var payload redundancy.ActivationFailed
	require.NoError(t, json.Unmarshal(failed.Data, &payload))
	require.Equal(t, notServing.Error(), payload.Error, "the original failure is not summarized away")
	require.Equal(t, redundancy.ActivationInitial, payload.Kind)
	require.GreaterOrEqual(t, payload.DurationMS, int64(0))
}

// TestContendStopsWhenItCannotStateWhatItDid checks the error policy: ownership
// is a startup path with an error to return, so a publication failure is
// returned rather than swallowed. A machine whose ownership moved with no record
// that it did is not a state an operator can be asked to reason about.
func TestContendStopsWhenItCannotStateWhatItDid(t *testing.T) {
	t.Parallel()

	recordUnwritable := errors.New("jsonl: write events.jsonl: disk is full")
	lease := openLease(t, leaseConfig(t), redundancy.RolePrimary)
	runtime := newRecordingRuntime()
	publisher := failing(t, recordUnwritable)

	err := redundancy.Contend(t.Context(), publisher, lease, unhealthyPeer, runtime.runtime())

	require.ErrorIs(t, err, recordUnwritable)
	require.Empty(t, runtime.recorded(), "an instance that cannot state that it took ownership never activates")
}

func TestContendRequiresAPublisher(t *testing.T) {
	t.Parallel()

	err := redundancy.Contend(t.Context(), nil, nil, redundancy.Deps{}, newRecordingRuntime().runtime())

	require.ErrorContains(t, err, "publisher is required")
}

// captureBackend is the storage.Backend a test composes a real publisher over.
// Testing at the backend rather than at events.Publisher is what keeps the
// assertions honest: the events an operator reads are stamped envelopes, and
// stamping is exactly what a fake publisher would skip.
type captureBackend struct {
	mu        sync.Mutex
	err       error
	envelopes []events.Envelope
}

// Store records the envelope, or refuses it when the test asked the local record
// to fail.
func (b *captureBackend) Store(_ context.Context, envelope events.Envelope) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return b.err
	}
	b.envelopes = append(b.envelopes, envelope)
	return nil
}

// Close satisfies storage.Backend. There is nothing to release.
func (b *captureBackend) Close(context.Context) error { return nil }

// recorded returns what has been stored so far.
func (b *captureBackend) recorded() []events.Envelope {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.envelopes)
}

// recording composes the process-local publisher a Contend call is given, and a
// function that reads back the envelopes it stamped.
func recording(t *testing.T) (local events.Publisher, recorded func() []events.Envelope) {
	t.Helper()
	factory, err := events.NewFactory(config.Descriptor{
		Platform: "opdl", Project: "scenario", Environment: "development",
		Site: "local", Machine: "node", MachineProfile: "all-in-one",
	}, redundancy.RolePrimary.String())
	require.NoError(t, err)
	backend := &captureBackend{}
	publisher, err := storage.NewPublisher(factory, backend)
	require.NoError(t, err)

	return publisher, func() []events.Envelope {
		envelopes := backend.recorded()
		for _, envelope := range envelopes {
			require.NoError(t, envelope.Validate(), "a recorded event is a complete envelope")
		}
		return envelopes
	}
}

// failing composes a process-local publisher whose backend refuses everything,
// which is what an unwritable local record looks like to a producer.
func failing(t *testing.T, cause error) events.Publisher {
	t.Helper()
	factory, err := events.NewFactory(config.Descriptor{
		Platform: "opdl", Project: "scenario", Environment: "development",
		Site: "local", Machine: "node", MachineProfile: "all-in-one",
	}, redundancy.RolePrimary.String())
	require.NoError(t, err)
	publisher, err := storage.NewPublisher(factory, &captureBackend{err: cause})
	require.NoError(t, err)
	return publisher
}

// types lists the kinds of a recorded run, which is what an assertion about
// order is actually about.
func types(envelopes []events.Envelope) []events.Type {
	kinds := make([]events.Type, 0, len(envelopes))
	for _, envelope := range envelopes {
		kinds = append(kinds, envelope.Type)
	}
	return kinds
}
