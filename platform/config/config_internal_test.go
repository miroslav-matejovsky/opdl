package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Load reads whatever descriptor is compiled into the binary, so the rejection
// of an unusable duration is tested against composed values here rather than by
// building a binary per case.
//
// The descriptor's own decoder already refuses a duration that will not parse.
// What these cover is the part it cannot: a value that parses to zero or less.
// Zero is the dangerous one, because http.Server reads a zero read-header
// timeout as no limit at all, which is indistinguishable from an omission.

func TestTimeoutsOfRejectsUnusableDurations(t *testing.T) {
	tests := map[string]Instance{
		"read header timeout is zero": {
			APIReadHeaderTimeout: "0s",
			APIShutdownTimeout:   "10s",
		},
		"read header timeout is negative": {
			APIReadHeaderTimeout: "-1s",
			APIShutdownTimeout:   "10s",
		},
		"shutdown timeout is zero": {
			APIReadHeaderTimeout: "5s",
			APIShutdownTimeout:   "0s",
		},
		"read header timeout is absent": {
			APIShutdownTimeout: "10s",
		},
	}
	for name, instance := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := timeoutsOf(RolePrimary, &instance)
			require.ErrorContains(t, err, "primary.api_")
		})
	}
}

func TestTimeoutsOfReadsADeployedInstance(t *testing.T) {
	timeouts, err := timeoutsOf(RoleStandby, &Instance{
		APIReadHeaderTimeout: "5s",
		APIShutdownTimeout:   "10s",
	})
	require.NoError(t, err)
	require.Equal(t, 5*time.Second, timeouts.readHeader)
	require.Equal(t, 10*time.Second, timeouts.shutdown)
}

// TestTimeoutsOfSkipsAnInstanceThatIsNotDeployed checks an instance the machine
// does not run, and therefore has no record for, is not required to carry
// timeouts for a listener it never binds.
func TestTimeoutsOfSkipsAnInstanceThatIsNotDeployed(t *testing.T) {
	timeouts, err := timeoutsOf(RoleStandby, nil)
	require.NoError(t, err)
	require.Zero(t, timeouts)
}

func TestLagBoundOfReadsTheLease(t *testing.T) {
	bound, err := lagBoundOf(&Lease{LagBound: "3s"})
	require.NoError(t, err)
	require.Equal(t, 3*time.Second, bound)

	for _, stated := range []string{"", "0s", "-1s"} {
		_, err := lagBoundOf(&Lease{LagBound: stated})
		require.ErrorContains(t, err, "lease.lag_bound")
	}
}

// TestLagBoundOfWithoutALease checks a standby-less machine has no failover
// bound rather than a zero one it has to defend.
func TestLagBoundOfWithoutALease(t *testing.T) {
	bound, err := lagBoundOf(nil)
	require.NoError(t, err)
	require.Zero(t, bound)
}
