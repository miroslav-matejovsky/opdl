package nats

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/operations"
)

// errBoom stands in for whatever made a loop give up.
var errBoom = errors.New("boom")

func TestAdapterEventsDeclareTheirContract(t *testing.T) {
	tests := []struct {
		name     string
		event    events.Event
		wantType events.Type
		want     events.Severity
		wantJSON string
	}{
		{
			name: "server starting",
			event: ServerStarting{
				ClientAddress: "127.0.0.1:4222", ClusterAddress: "127.0.0.1:6222",
				Routes: []string{"127.0.0.1:6223"}, DataDir: `C:\data`,
			},
			wantType: TypeServerStarting,
			want:     events.SeverityInfo,
			wantJSON: `{"client_address":"127.0.0.1:4222","cluster_address":"127.0.0.1:6222","routes":["127.0.0.1:6223"],"data_dir":"C:\\data"}`,
		},
		{
			name:     "server ready",
			event:    ServerReady{ClientAddress: "127.0.0.1:4222"},
			wantType: TypeServerReady,
			want:     events.SeverityInfo,
			wantJSON: `{"client_address":"127.0.0.1:4222"}`,
		},
		{
			name:     "client connected",
			event:    ClientConnected{Server: "nats://127.0.0.1:4222", Attempts: 3, DurationMS: 42},
			wantType: TypeClientConnected,
			want:     events.SeverityInfo,
			wantJSON: `{"server":"nats://127.0.0.1:4222","attempts":3,"duration_ms":42}`,
		},
		{
			name:     "client connect retry",
			event:    ClientConnectRetry{Servers: []string{"127.0.0.1:4222"}, Attempt: 2, Error: "connection refused"},
			wantType: TypeClientConnectRetry,
			want:     events.SeverityWarn,
			wantJSON: `{"servers":["127.0.0.1:4222"],"attempt":2,"error":"connection refused"}`,
		},
		{
			name:     "client disconnected unexpectedly",
			event:    ClientDisconnected{LastServer: "nats://127.0.0.1:4222", Error: "broken pipe"},
			wantType: TypeClientDisconnected,
			want:     events.SeverityWarn,
			wantJSON: `{"last_server":"nats://127.0.0.1:4222","shutdown":false,"error":"broken pipe"}`,
		},
		{
			name:     "client disconnected for shutdown",
			event:    ClientDisconnected{LastServer: "nats://127.0.0.1:4222", Shutdown: true},
			wantType: TypeClientDisconnected,
			want:     events.SeverityInfo,
			wantJSON: `{"last_server":"nats://127.0.0.1:4222","shutdown":true}`,
		},
		{
			name:     "client reconnected",
			event:    ClientReconnected{Server: "nats://127.0.0.1:4223"},
			wantType: TypeClientReconnected,
			want:     events.SeverityInfo,
			wantJSON: `{"server":"nats://127.0.0.1:4223"}`,
		},
		{
			name:     "client closed",
			event:    ClientClosed{Error: "connection closed"},
			wantType: TypeClientClosed,
			want:     events.SeverityInfo,
			wantJSON: `{"error":"connection closed"}`,
		},
		{
			name:     "client async error",
			event:    ClientAsyncError{Subject: "opdl.abc.event.registration.proposed", Error: "slow consumer"},
			wantType: TypeClientAsyncError,
			want:     events.SeverityError,
			wantJSON: `{"subject":"opdl.abc.event.registration.proposed","error":"slow consumer"}`,
		},
		{
			name:     "journal ready",
			event:    JournalReady{Journal: "opdl_abc", HostsStorage: true, Replicas: 3},
			wantType: TypeJournalReady,
			want:     events.SeverityInfo,
			wantJSON: `{"journal":"opdl_abc","hosts_storage":true,"replicas":3}`,
		},
		{
			name:     "journal retry",
			event:    JournalRetry{Journal: "opdl_abc", Attempt: 4, Error: "no responders"},
			wantType: TypeJournalRetry,
			want:     events.SeverityWarn,
			wantJSON: `{"journal":"opdl_abc","attempt":4,"error":"no responders"}`,
		},
		{
			name:     "journal recovered",
			event:    JournalRecovered{Journal: "opdl_abc", Attempts: 4, DurationMS: 800},
			wantType: TypeJournalRecovered,
			want:     events.SeverityInfo,
			wantJSON: `{"journal":"opdl_abc","attempts":4,"duration_ms":800}`,
		},
		{
			name:     "projector started",
			event:    ProjectorStarted{},
			wantType: TypeProjectorStarted,
			want:     events.SeverityInfo,
			wantJSON: `{}`,
		},
		{
			name:     "projector reset",
			event:    ProjectorReset{NextSequence: 12},
			wantType: TypeProjectorReset,
			want:     events.SeverityWarn,
			wantJSON: `{"next_sequence":12}`,
		},
		{
			name:     "projector attach retry",
			event:    ProjectorAttachRetry{NextSequence: 12, Attempt: 2, Error: "leadership changed"},
			wantType: TypeProjectorAttachRetry,
			want:     events.SeverityWarn,
			wantJSON: `{"next_sequence":12,"attempt":2,"error":"leadership changed"}`,
		},
		{
			name:     "projector canceled",
			event:    ProjectorStopped{},
			wantType: TypeProjectorStopped,
			want:     events.SeverityInfo,
			wantJSON: `{}`,
		},
		{
			name:     "projector gave up",
			event:    ProjectorStopped{Error: "apply failed"},
			wantType: TypeProjectorStopped,
			want:     events.SeverityError,
			wantJSON: `{"error":"apply failed"}`,
		},
		{
			name:     "handler started",
			event:    HandlerStarted{Handler: "registration"},
			wantType: TypeHandlerStarted,
			want:     events.SeverityInfo,
			wantJSON: `{"handler":"registration"}`,
		},
		{
			name:     "handler reset",
			event:    HandlerReset{Handler: "registration"},
			wantType: TypeHandlerReset,
			want:     events.SeverityWarn,
			wantJSON: `{"handler":"registration"}`,
		},
		{
			name:     "handler attach retry",
			event:    HandlerAttachRetry{Handler: "abc_node_registration_v1", Attempt: 3, Error: "no responders"},
			wantType: TypeHandlerAttachRetry,
			want:     events.SeverityWarn,
			wantJSON: `{"handler":"abc_node_registration_v1","attempt":3,"error":"no responders"}`,
		},
		{
			name:     "handler canceled",
			event:    HandlerStopped{Handler: "registration"},
			wantType: TypeHandlerStopped,
			want:     events.SeverityInfo,
			wantJSON: `{"handler":"registration"}`,
		},
		{
			name:     "handler gave up",
			event:    HandlerStopped{Handler: "registration", Error: "delivery exhausted"},
			wantType: TypeHandlerStopped,
			want:     events.SeverityError,
			wantJSON: `{"handler":"registration","error":"delivery exhausted"}`,
		},
		{
			name:     "consumer heartbeat missed",
			event:    ConsumerHeartbeatMissed{Stream: "opdl_abc", Error: "no heartbeat received"},
			wantType: TypeConsumerHeartbeatMissed,
			want:     events.SeverityWarn,
			wantJSON: `{"stream":"opdl_abc","error":"no heartbeat received"}`,
		},
		{
			name:     "stopping",
			event:    Stopping{},
			wantType: TypeStopping,
			want:     events.SeverityInfo,
			wantJSON: `{}`,
		},
		{
			name:     "stopped cleanly",
			event:    Stopped{},
			wantType: TypeStopped,
			want:     events.SeverityInfo,
			wantJSON: `{}`,
		},
		{
			name:     "stopped failing",
			event:    Stopped{Error: "server did not stop"},
			wantType: TypeStopped,
			want:     events.SeverityError,
			wantJSON: `{"error":"server did not stop"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.wantType, test.event.EventType())
			require.NoError(t, test.event.EventType().Validate())
			require.Equal(t, "nats", test.event.EventType().Source(),
				"every fact in this catalog is stated by the adapter")

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

// TestAdapterRecordsItsLifecycleLocally runs a real adapter through open and
// close with a real recorder, so what an operator would read is what is
// asserted. It also pins the reliability rule: the disconnect and close
// callbacks fire while the connection is going away, and they still record,
// because they record locally rather than through the journal they describe.
func TestAdapterRecordsItsLifecycleLocally(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping NATS integration test in -short mode")
	}
	ctx, recorded := recordingContext(t)
	f, err := Open(ctx, testDescriptor, testConfig(t))
	require.NoError(t, err)
	require.NoError(t, f.Close(ctx))

	// The client's callbacks run on its own goroutines, so the closing pair
	// arrives shortly after Close returns.
	waitFor(t, func() bool {
		for _, envelope := range recorded() {
			if envelope.Type == TypeClientClosed {
				return true
			}
		}
		return false
	}, "the client never recorded that it closed")

	envelopes := recorded()
	byType := map[events.Type]events.Envelope{}
	for _, envelope := range envelopes {
		require.NoError(t, envelope.Validate(), "a recorded adapter event is a complete envelope")
		require.Equal(t, "nats", envelope.Source)
		byType[envelope.Type] = envelope
	}
	for _, want := range []events.Type{
		TypeServerStarting, TypeServerReady, TypeClientConnected,
		TypeJournalReady, TypeStopping, TypeStopped, TypeClientDisconnected, TypeClientClosed,
	} {
		require.Contains(t, byType, want)
	}

	var connected ClientConnected
	require.NoError(t, json.Unmarshal(byType[TypeClientConnected].Data, &connected))
	require.NotEmpty(t, connected.Server)
	require.NotContains(t, connected.Server, "@",
		"the connected server is recorded redacted, so a credentialed URL cannot leak")
	require.Positive(t, connected.Attempts)

	var disconnected ClientDisconnected
	require.NoError(t, json.Unmarshal(byType[TypeClientDisconnected].Data, &disconnected))
	require.True(t, disconnected.Shutdown, "this node asked for the disconnect")
	require.Equal(t, events.SeverityInfo, byType[TypeClientDisconnected].Severity,
		"an expected disconnect is routine, an unasked-for one is not")
	require.NotContains(t, disconnected.LastServer, "@")

	require.Equal(t, events.SeverityInfo, byType[TypeStopped].Severity, "a clean release is routine")
}

// recordingContext returns a context carrying a real recorder, and a function
// that reads back the envelopes written so far.
func recordingContext(t *testing.T) (ctx context.Context, recorded func() []events.Envelope) {
	t.Helper()
	factory, err := events.NewFactory(testDescriptor, "primary")
	require.NoError(t, err)
	recorder, err := operations.Open(t.TempDir(), factory)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, recorder.Close()) })

	return operations.WithRecorder(context.Background(), recorder), func() []events.Envelope {
		data, err := os.ReadFile(recorder.Path())
		require.NoError(t, err)
		var envelopes []events.Envelope
		for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			envelope, err := events.Decode([]byte(line))
			require.NoError(t, err)
			envelopes = append(envelopes, envelope)
		}
		return envelopes
	}
}

func TestErrorTextDistinguishesACancelledLoopFromAFailedOne(t *testing.T) {
	require.Empty(t, errorText(nil), "a loop that was asked to stop reports no error")
	require.Equal(t, "boom", errorText(errBoom))
	require.Equal(t, events.SeverityInfo, ProjectorStopped{Error: errorText(nil)}.Severity())
	require.Equal(t, events.SeverityError, ProjectorStopped{Error: errorText(errBoom)}.Severity())
}
