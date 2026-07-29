package app

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/instance/state"
)

func TestApplicationEventsDeclareTheirContract(t *testing.T) {
	tests := []struct {
		name     string
		event    events.Event
		wantType events.Type
		want     events.Severity
	}{
		{name: "process started", event: ProcessStarted{}, wantType: TypeProcessStarted, want: events.SeverityInfo},
		{
			name:     "process stopped cleanly",
			event:    ProcessStopped{},
			wantType: TypeProcessStopped,
			want:     events.SeverityInfo,
		},
		{
			name:     "process stopped failing",
			event:    ProcessStopped{Error: "boom"},
			wantType: TypeProcessStopped,
			want:     events.SeverityError,
		},
		{name: "lease open failed", event: LeaseOpenFailed{}, wantType: TypeLeaseOpenFailed, want: events.SeverityError},
		{name: "api listen failed", event: APIListenFailed{}, wantType: TypeAPIListenFailed, want: events.SeverityError},
		{name: "api listening", event: APIListening{}, wantType: TypeAPIListening, want: events.SeverityInfo},
		{name: "api active", event: APIActive{}, wantType: TypeAPIActive, want: events.SeverityInfo},
		{name: "api stopped cleanly", event: APIStopped{}, wantType: TypeAPIStopped, want: events.SeverityInfo},
		{name: "api stopped failing", event: APIStopped{Error: "boom"}, wantType: TypeAPIStopped, want: events.SeverityError},
		{
			name:     "event fabric started",
			event:    EventFabricStarted{},
			wantType: TypeEventFabricStarted,
			want:     events.SeverityInfo,
		},
		{
			name:     "event fabric start failed",
			event:    EventFabricStartFailed{},
			wantType: TypeEventFabricStartFailed,
			want:     events.SeverityError,
		},
		{name: "standby waiting", event: StandbyWaiting{}, wantType: TypeStandbyWaiting, want: events.SeverityInfo},
	}
	// The scope assertion below goes through a real stamper rather than asking
	// the event, because what this catalog claims is that it declares no scope at
	// all: reading the default back off a stamped envelope is what proves the
	// silence resolves the way the catalog header says it does.
	factory, err := events.NewFactory(testDescriptor, "primary")
	require.NoError(t, err)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.wantType, test.event.EventType())
			require.NoError(t, test.event.EventType().Validate())
			require.Equal(t, "app", test.event.EventType().Source(),
				"every fact in this catalog is stated by the application runtime")

			severity := events.SeverityInfo
			if severe, ok := test.event.(events.Severe); ok {
				severity = severe.Severity()
			}
			require.Equal(t, test.want, severity)

			envelope, err := factory.Wrap(t.Context(), test.event)
			require.NoError(t, err)
			require.Equal(t, events.ScopeInstance, envelope.Scope,
				"every fact in this catalog is about one process, so none of them leaves it")
		})
	}
}

// TestApplicationEventsAreStampedIntoValidEnvelopes proves the catalog is
// usable: every event in it stamps into an envelope a reader can replay, with
// no metadata repeated in the payload.
func TestApplicationEventsAreStampedIntoValidEnvelopes(t *testing.T) {
	factory, err := events.NewFactory(testDescriptor, "primary")
	require.NoError(t, err)

	failure, err := factory.Wrap(t.Context(), APIStopped{Error: "listener closed"})
	require.NoError(t, err)
	require.NoError(t, failure.Validate())
	require.Equal(t, events.SeverityError, failure.Severity)
	require.JSONEq(t, `{"error":"listener closed"}`, string(failure.Data),
		"a failure keeps its error, and repeats no envelope metadata")

	waiting, err := factory.Wrap(t.Context(), StandbyWaiting{})
	require.NoError(t, err)
	require.NoError(t, waiting.Validate())
	require.JSONEq(t, `{}`, string(waiting.Data), "the fact is the whole of it; who it is about is the origin")
	require.Equal(t, "node", waiting.Origin.Machine)
	require.Equal(t, "primary", waiting.Origin.ProcessRole)

	started, err := factory.Wrap(t.Context(), ProcessStarted{
		EventsFile:        `D:\opdl\events\events.jsonl`,
		MachineEventsFile: `D:\opdl\events\machine-events.jsonl`,
		StateFile:         `D:\opdl\state.json`,
		Epoch:             3,
	})
	require.NoError(t, err)
	require.NoError(t, started.Validate())
	require.JSONEq(t, `{"events_file":"D:\\opdl\\events\\events.jsonl","machine_events_file":"D:\\opdl\\events\\machine-events.jsonl","state_file":"D:\\opdl\\state.json","epoch":3,"standby_enabled":false}`, string(started.Data),
		"a started process names every file it opened before it could state anything, and which incarnation of the instance it is")

	// The epoch facts carry the counter and why it moved, so a reader of the
	// record can order incarnations without holding the state file open.
	advanced, err := factory.Wrap(t.Context(), EpochAdvanced{
		StateFile:       `D:\opdl\state.json`,
		Epoch:           4,
		Reason:          string(state.ReasonActivated),
		ProcessEpoch:    2,
		ActivationEpoch: 2,
	})
	require.NoError(t, err)
	require.NoError(t, advanced.Validate())
	require.Equal(t, events.SeverityInfo, advanced.Severity)
	require.JSONEq(t, `{"state_file":"D:\\opdl\\state.json","epoch":4,"reason":"activated","process_epoch":2,"activation_epoch":2}`, string(advanced.Data),
		"the total says the incarnation is a different one; the two counts say what made it one")

	failedEpoch, err := factory.Wrap(t.Context(), EpochAdvanceFailed{
		StateFile: `D:\opdl\state.json`,
		Reason:    string(state.ReasonProcessStarted),
		Error:     "disk full",
	})
	require.NoError(t, err)
	require.NoError(t, failedEpoch.Validate())
	require.Equal(t, events.SeverityError, failedEpoch.Severity,
		"an instance that cannot record its incarnation stops, so this is not a degradation")
}
