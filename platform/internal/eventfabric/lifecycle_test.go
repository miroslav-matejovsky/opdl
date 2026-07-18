package eventfabric

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLifecycleEventsCarryProcessIdentityAndState(t *testing.T) {
	ready := NewReady(Info{Adapter: "nats"}, 42, "standby")
	require.Equal(t, "standby", ready.ProcessRole)
	require.Equal(t, "active", ready.ProcessState)
	require.Equal(t, uint64(42), ready.HighWater)

	stopping := NewStopping("nats", "primary")
	require.Equal(t, "primary", stopping.ProcessRole)
	require.Equal(t, "stopping", stopping.ProcessState)
}
