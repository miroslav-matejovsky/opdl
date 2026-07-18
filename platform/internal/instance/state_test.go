package instance_test

import (
	"slices"
	"testing"

	"github.com/miroslav-matejovsky/opdl/platform/internal/instance"
	"github.com/stretchr/testify/require"
)

func TestStateStringAndValid(t *testing.T) {
	t.Parallel()

	all := []instance.State{
		instance.StateStarting,
		instance.StateStandby,
		instance.StateActivating,
		instance.StateActive,
		instance.StateStopping,
		instance.StateFailed,
	}
	for _, s := range all {
		require.True(t, s.Valid(), "%s should be valid", s)
		require.NotEmpty(t, s.String())
	}
	require.False(t, instance.State("running").Valid())
	require.False(t, instance.State("").Valid())
}

func TestStateActive(t *testing.T) {
	t.Parallel()

	require.True(t, instance.StateActive.Active())
	require.False(t, instance.StateStandby.Active())
	require.False(t, instance.StateActivating.Active())
	require.False(t, instance.StateStopping.Active())
}

func TestStateCanTransition(t *testing.T) {
	t.Parallel()

	allowed := map[instance.State][]instance.State{
		instance.StateStarting:   {instance.StateStandby, instance.StateActivating, instance.StateStopping, instance.StateFailed},
		instance.StateStandby:    {instance.StateActivating, instance.StateStopping, instance.StateFailed},
		instance.StateActivating: {instance.StateActive, instance.StateStopping, instance.StateFailed},
		instance.StateActive:     {instance.StateStopping, instance.StateFailed},
		instance.StateStopping:   {instance.StateFailed},
		instance.StateFailed:     {},
	}
	all := []instance.State{
		instance.StateStarting, instance.StateStandby, instance.StateActivating,
		instance.StateActive, instance.StateStopping, instance.StateFailed,
	}
	for from, nexts := range allowed {
		for _, to := range all {
			want := slices.Contains(nexts, to)
			require.Equalf(t, want, from.CanTransition(to), "%s -> %s", from, to)
		}
	}
}

// A standby must not jump straight to active: it activates first, composing its
// active resources, before it owns any active capability.
func TestStandbyCannotJumpToActive(t *testing.T) {
	t.Parallel()

	require.False(t, instance.StateStandby.CanTransition(instance.StateActive))
	require.True(t, instance.StateStandby.CanTransition(instance.StateActivating))
	require.True(t, instance.StateActivating.CanTransition(instance.StateActive))
}
