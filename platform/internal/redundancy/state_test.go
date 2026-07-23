package redundancy_test

import (
	"testing"

	"github.com/miroslav-matejovsky/opdl/platform/internal/redundancy"
	"github.com/stretchr/testify/require"
)

func TestStateStringAndValid(t *testing.T) {
	t.Parallel()

	for _, s := range []redundancy.State{redundancy.StatePassive, redundancy.StateActive} {
		require.True(t, s.Valid(), "%s should be valid", s)
		require.NotEmpty(t, s.String())
	}
	require.False(t, redundancy.State("running").Valid())
	require.False(t, redundancy.State("").Valid())
	// The transitional states the status file used to report are gone. Each of
	// them is a fact in the local record now, not a token an instance sits in.
	require.False(t, redundancy.State("activating").Valid())
	require.False(t, redundancy.State("stopping").Valid())
}

func TestStateActive(t *testing.T) {
	t.Parallel()

	require.True(t, redundancy.StateActive.Active())
	require.False(t, redundancy.StatePassive.Active())
}

// TestStateTokensMatchTheAPI checks the two tokens an instance reports through
// its own API are the two this type defines. A reader that correlates the API
// with the local record must not have to translate between them.
func TestStateTokensMatchTheAPI(t *testing.T) {
	t.Parallel()

	require.Equal(t, "active", redundancy.StateActive.String())
	require.Equal(t, "passive", redundancy.StatePassive.String())
}
