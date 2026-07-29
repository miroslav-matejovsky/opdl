package servicehealth

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// The retry policy is the whole of what this package decides, so it is tested
// as a table with no clock, no transport, and nothing to wait for. Everything
// below this file is scheduling and plumbing around these transitions.

var errProbe = errors.New("connection refused")

// TestTrackerFoldsAttemptsIntoAStableStatus walks the state table one attempt at
// a time: what the target is reported as, and how many failures are pending
// behind that report.
func TestTrackerFoldsAttemptsIntoAStableStatus(t *testing.T) {
	// step is one attempt and what the target should read as afterwards.
	type step struct {
		err         error
		wantStatus  Status
		wantPending int
		wantBecause string
	}
	tests := map[string]struct {
		retries int
		steps   []step
	}{
		"a target nobody has asked is unknown until an attempt resolves it": {
			retries: 2,
			steps: []step{
				{err: errProbe, wantStatus: StatusUnknown, wantPending: 1,
					wantBecause: "one failure below the threshold has not decided anything yet"},
				{err: errProbe, wantStatus: StatusUnhealthy, wantPending: 2,
					wantBecause: "the second consecutive failure reaches retries"},
			},
		},
		"one success is enough to become healthy": {
			retries: 3,
			steps: []step{
				{err: nil, wantStatus: StatusHealthy, wantPending: 0},
			},
		},
		"a healthy target stays healthy while failures are below the threshold": {
			retries: 3,
			steps: []step{
				{err: nil, wantStatus: StatusHealthy, wantPending: 0},
				{err: errProbe, wantStatus: StatusHealthy, wantPending: 1,
					wantBecause: "a single missed request is noise, and the pending count is what shows it"},
				{err: errProbe, wantStatus: StatusHealthy, wantPending: 2},
				{err: errProbe, wantStatus: StatusUnhealthy, wantPending: 3,
					wantBecause: "the third consecutive failure reaches retries"},
			},
		},
		"recovery is immediate and is not debounced the way failure is": {
			retries: 2,
			steps: []step{
				{err: errProbe, wantStatus: StatusUnknown, wantPending: 1},
				{err: errProbe, wantStatus: StatusUnhealthy, wantPending: 2},
				{err: nil, wantStatus: StatusHealthy, wantPending: 0,
					wantBecause: "a service that answered is answering"},
			},
		},
		"a success resets the count so failures must be consecutive": {
			retries: 3,
			steps: []step{
				{err: errProbe, wantStatus: StatusUnknown, wantPending: 1},
				{err: errProbe, wantStatus: StatusUnknown, wantPending: 2},
				{err: nil, wantStatus: StatusHealthy, wantPending: 0},
				{err: errProbe, wantStatus: StatusHealthy, wantPending: 1,
					wantBecause: "the earlier two failures were interrupted and do not carry over"},
				{err: errProbe, wantStatus: StatusHealthy, wantPending: 2},
			},
		},
		"one retry means the first failure decides it": {
			retries: 1,
			steps: []step{
				{err: errProbe, wantStatus: StatusUnhealthy, wantPending: 1,
					wantBecause: "a service that tolerates no failure is failed by the first"},
			},
		},
		"the pending count stops at the threshold rather than climbing forever": {
			retries: 2,
			steps: []step{
				{err: errProbe, wantStatus: StatusUnknown, wantPending: 1},
				{err: errProbe, wantStatus: StatusUnhealthy, wantPending: 2},
				{err: errProbe, wantStatus: StatusUnhealthy, wantPending: 2,
					wantBecause: "past the threshold the status carries the fact and the count adds nothing"},
				{err: errProbe, wantStatus: StatusUnhealthy, wantPending: 2},
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			track := newTracker(test.retries)
			require.Equal(t, StatusUnknown, track.status, "a target starts unknown, before anything has asked it")

			for i, step := range test.steps {
				status, pending := track.record(step.err)
				require.Equalf(t, step.wantStatus, status, "attempt %d status: %s", i+1, step.wantBecause)
				require.Equalf(t, step.wantPending, pending, "attempt %d pending failures: %s", i+1, step.wantBecause)
			}
		})
	}
}
