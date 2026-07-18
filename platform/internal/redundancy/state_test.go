package redundancy_test

import (
	"slices"
	"testing"

	"github.com/miroslav-matejovsky/opdl/platform/internal/redundancy"
	"github.com/stretchr/testify/require"
)

func TestStateStringAndValid(t *testing.T) {
	t.Parallel()

	all := []redundancy.State{
		redundancy.StateStarting,
		redundancy.StateStandby,
		redundancy.StateActivating,
		redundancy.StateActive,
		redundancy.StateStopping,
		redundancy.StateFailed,
	}
	for _, s := range all {
		require.True(t, s.Valid(), "%s should be valid", s)
		require.NotEmpty(t, s.String())
	}
	require.False(t, redundancy.State("running").Valid())
	require.False(t, redundancy.State("").Valid())
}

func TestStateActive(t *testing.T) {
	t.Parallel()

	require.True(t, redundancy.StateActive.Active())
	require.False(t, redundancy.StateStandby.Active())
	require.False(t, redundancy.StateActivating.Active())
	require.False(t, redundancy.StateStopping.Active())
}

func TestStateCanTransition(t *testing.T) {
	t.Parallel()

	allowed := map[redundancy.State][]redundancy.State{
		redundancy.StateStarting:   {redundancy.StateStandby, redundancy.StateActivating, redundancy.StateStopping, redundancy.StateFailed},
		redundancy.StateStandby:    {redundancy.StateActivating, redundancy.StateStopping, redundancy.StateFailed},
		redundancy.StateActivating: {redundancy.StateActive, redundancy.StateStopping, redundancy.StateFailed},
		redundancy.StateActive:     {redundancy.StateStopping, redundancy.StateFailed},
		redundancy.StateStopping:   {redundancy.StateFailed},
		redundancy.StateFailed:     {},
	}
	all := []redundancy.State{
		redundancy.StateStarting, redundancy.StateStandby, redundancy.StateActivating,
		redundancy.StateActive, redundancy.StateStopping, redundancy.StateFailed,
	}
	for from, nexts := range allowed {
		for _, to := range all {
			want := slices.Contains(nexts, to)
			require.Equalf(t, want, from.CanTransition(to), "%s -> %s", from, to)
		}
	}
}

// TestStandbyCannotJumpToActive checks a standby must activate first, composing
// its active resources, before it owns any active capability.
func TestStandbyCannotJumpToActive(t *testing.T) {
	t.Parallel()

	require.False(t, redundancy.StateStandby.CanTransition(redundancy.StateActive))
	require.True(t, redundancy.StateStandby.CanTransition(redundancy.StateActivating))
	require.True(t, redundancy.StateActivating.CanTransition(redundancy.StateActive))
}
