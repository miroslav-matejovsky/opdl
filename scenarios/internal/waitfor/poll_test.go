package waitfor

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPollImmediateCondition(t *testing.T) {
	err := Poll(t.Context(), time.Second, 10*time.Millisecond, func() bool {
		return true
	}, nil)
	require.NoError(t, err)
}

func TestPollImmediateAbort(t *testing.T) {
	err := Poll(t.Context(), time.Second, 10*time.Millisecond, func() bool {
		return false
	}, func() (bool, string) {
		return true, "process died immediately"
	})
	var abortErr *AbortError
	require.True(t, errors.As(err, &abortErr))
	require.Equal(t, "process died immediately", abortErr.Reason)
}

func TestPollSatisfiedAfterN(t *testing.T) {
	var count atomic.Int32
	err := Poll(t.Context(), time.Second, 5*time.Millisecond, func() bool {
		return count.Add(1) >= 3
	}, nil)
	require.NoError(t, err)
	require.GreaterOrEqual(t, count.Load(), int32(3))
}

func TestPollTimeout(t *testing.T) {
	err := Poll(t.Context(), 20*time.Millisecond, 5*time.Millisecond, func() bool {
		return false
	}, nil)
	var timeoutErr *TimeoutError
	require.True(t, errors.As(err, &timeoutErr))
	require.Equal(t, 20*time.Millisecond, timeoutErr.Timeout)
}

func TestPollAbortCheckedBeforeDeadline(t *testing.T) {
	var aborted atomic.Bool
	// Simulate abort occurring just as the timeout expires
	go func() {
		time.Sleep(15 * time.Millisecond)
		aborted.Store(true)
	}()

	err := Poll(t.Context(), 20*time.Millisecond, 5*time.Millisecond, func() bool {
		return false
	}, func() (bool, string) {
		if aborted.Load() {
			return true, "process dead at timeout"
		}
		return false, ""
	})
	var abortErr *AbortError
	require.True(t, errors.As(err, &abortErr))
	require.Equal(t, "process dead at timeout", abortErr.Reason)
}

func TestPollContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := Poll(ctx, time.Second, 10*time.Millisecond, func() bool {
		return false
	}, nil)
	require.ErrorIs(t, err, context.Canceled)
}
