package eventfabric

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

func TestLifecycleEventsCarryProcessIdentityAndState(t *testing.T) {
	ready := NewReady(Info{Adapter: "nats"}, 42, "standby")
	require.Equal(t, "standby", ready.InstanceRole)
	require.Equal(t, "active", ready.ProcessState)
	require.Equal(t, uint64(42), ready.HighWater)

	stopping := NewStopping("nats", "primary")
	require.Equal(t, "primary", stopping.InstanceRole)
	require.Equal(t, "stopping", stopping.ProcessState)
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
