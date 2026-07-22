package eventfabric

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// TestLifecycleEventsCarryOnlyWhatTheEnvelopeDoesNot checks the payloads state
// the node's transport and its catch-up point, and nothing the envelope already
// carries: which process stated the fact is its origin, and what the process was
// doing is the event type.
func TestLifecycleEventsCarryOnlyWhatTheEnvelopeDoesNot(t *testing.T) {
	ready := Ready{Info: Info{Adapter: "nats", Journal: "opdl_abc"}, HighWater: 42}
	require.Equal(t, uint64(42), ready.HighWater)
	require.Equal(t, "nats", ready.Adapter)

	data, err := json.Marshal(ready)
	require.NoError(t, err)
	for _, absent := range []string{"process_role", "process_state", "pid", "machine"} {
		require.NotContains(t, string(data), absent, "the envelope states this, so the payload must not")
	}

	require.Equal(t, "nats", Stopping{Adapter: "nats"}.Adapter)
}

func TestLifecycleEventsDeclareTheirContract(t *testing.T) {
	var ready events.Event = Ready{}
	require.Equal(t, TypeReady, ready.EventType())
	require.Equal(t, TypeStopping, Stopping{}.EventType())

	// The source is derived from the type, and both events keep every default:
	// a lifecycle transition is routine, unversioned, untagged, and has no
	// identity beyond its occurrence.
	for _, eventType := range []events.Type{TypeReady, TypeStopping} {
		require.NoError(t, eventType.Validate())
		require.Equal(t, "event_fabric", eventType.Source())
	}
	require.NotImplements(t, (*events.Versioned)(nil), Ready{})
	require.NotImplements(t, (*events.Severe)(nil), Ready{})
	require.NotImplements(t, (*events.Tagged)(nil), Ready{})
	require.NotImplements(t, (*events.Identified)(nil), Ready{})
}
