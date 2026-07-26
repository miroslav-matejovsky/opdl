package redundancy_test

import (
	"testing"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/internal/machine/redundancy"
	"github.com/stretchr/testify/require"
)

func TestLagStateMeasuresContinuousLag(t *testing.T) {
	t.Parallel()

	var lag redundancy.LagState
	base := time.Now()

	// Caught up: no lag.
	require.Zero(t, lag.Observe(false, base))

	// Falls behind: lag is measured from the first behind observation.
	require.Zero(t, lag.Observe(true, base), "lag starts at zero the instant it falls behind")
	require.Equal(t, 2*time.Second, lag.Observe(true, base.Add(2*time.Second)))
	require.Equal(t, 5*time.Second, lag.Observe(true, base.Add(5*time.Second)))

	// Catching up resets the measurement, so a later lag does not accumulate the
	// earlier one.
	require.Zero(t, lag.Observe(false, base.Add(6*time.Second)))
	require.Zero(t, lag.Observe(true, base.Add(10*time.Second)))
	require.Equal(t, time.Second, lag.Observe(true, base.Add(11*time.Second)))
}

func TestExceeds(t *testing.T) {
	t.Parallel()

	require.False(t, redundancy.Exceeds(0, 5*time.Second))
	require.False(t, redundancy.Exceeds(5*time.Second, 5*time.Second), "equal is not exceeding")
	require.True(t, redundancy.Exceeds(6*time.Second, 5*time.Second))

	// A non-positive bound disables the check.
	require.False(t, redundancy.Exceeds(time.Hour, 0))
	require.False(t, redundancy.Exceeds(time.Hour, -time.Second))
}
