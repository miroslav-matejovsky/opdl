package redundancy_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/operations"
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
			name:     "lock opened",
			event:    redundancy.LockOpened{Object: "Global\\opdl", Existed: true},
			wantType: redundancy.TypeLockOpened,
			want:     events.SeverityInfo,
			wantJSON: `{"object":"Global\\opdl","existed":true}`,
		},
		{
			name:     "ownership waiting",
			event:    redundancy.OwnershipWaiting{Object: "Global\\opdl"},
			wantType: redundancy.TypeOwnershipWaiting,
			want:     events.SeverityInfo,
			wantJSON: `{"object":"Global\\opdl"}`,
		},
		{
			name:     "ownership handed over",
			event:    redundancy.OwnershipAcquired{Object: "Global\\opdl"},
			wantType: redundancy.TypeOwnershipAcquired,
			want:     events.SeverityInfo,
			wantJSON: `{"object":"Global\\opdl","abandoned":false}`,
		},
		{
			name:     "ownership abandoned by a dead process",
			event:    redundancy.OwnershipAcquired{Object: "Global\\opdl", Abandoned: true},
			wantType: redundancy.TypeOwnershipAcquired,
			want:     events.SeverityWarn,
			wantJSON: `{"object":"Global\\opdl","abandoned":true}`,
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
// through a real recorder, so what an operator would read is what is asserted:
// the typed events, in order, as canonical envelopes.
func TestContendRecordsTheOwnershipLifecycle(t *testing.T) {
	t.Parallel()

	lock := openLock(t, ownershipObject(t), redundancy.RolePrimary)
	runtime := newRecordingRuntime()
	ctx, recorded := recording(t)
	ctx, cancel := context.WithCancel(ctx)

	done := make(chan error, 1)
	go func() { done <- redundancy.Contend(ctx, lock, runtime.runtime()) }()
	require.Equal(t, redundancy.ActivationInitial, <-runtime.activeKinds)
	cancel()
	require.NoError(t, <-done)

	envelopes := recorded()
	require.Equal(t, []events.Type{
		redundancy.TypeLockOpened,
		redundancy.TypeOwnershipAcquired,
		redundancy.TypeActivationStarted,
		redundancy.TypeActivationCompleted,
	}, types(envelopes), "an uncontested start never waits, so it never says it did")

	acquired := envelopes[1]
	require.Equal(t, events.SeverityInfo, acquired.Severity, "nobody died; this was a free lock")
	require.Equal(t, "primary", acquired.Origin.ProcessRole, "which process took ownership is in the origin")
	var payload redundancy.OwnershipAcquired
	require.NoError(t, json.Unmarshal(acquired.Data, &payload))
	require.False(t, payload.Abandoned)
	require.Equal(t, lock.Name(), payload.Object)

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
	lock := openLock(t, ownershipObject(t), redundancy.RolePrimary)
	runtime := newRecordingRuntime()
	runtime.activeErr = notServing
	ctx, recorded := recording(t)

	require.ErrorIs(t, redundancy.Contend(ctx, lock, runtime.runtime()), notServing)

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

// recording returns a context carrying a real recorder, and a function that
// closes it and reads back the envelopes it wrote.
func recording(t *testing.T) (ctx context.Context, recorded func() []events.Envelope) {
	t.Helper()
	factory, err := events.NewFactory(config.Descriptor{
		Platform: "opdl", Project: "scenario", Environment: "development",
		Site: "local", Machine: "node", MachineProfile: "all-in-one",
	}, redundancy.RolePrimary.String())
	require.NoError(t, err)
	recorder, err := operations.Open(t.TempDir(), factory)
	require.NoError(t, err)

	return operations.WithRecorder(t.Context(), recorder), func() []events.Envelope {
		require.NoError(t, recorder.Close())
		data, err := os.ReadFile(recorder.Path())
		require.NoError(t, err)
		var envelopes []events.Envelope
		for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
			envelope, err := events.Decode([]byte(line))
			require.NoError(t, err)
			require.NoError(t, envelope.Validate(), "a recorded event is a complete envelope")
			envelopes = append(envelopes, envelope)
		}
		return envelopes
	}
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
