package waitfor

import (
	"context"
	"fmt"
	"time"
)

// Condition is polled until it reports true.
type Condition func() bool

// Abort reports that the condition can no longer become true, with a reason.
type Abort func() (aborted bool, reason string)

// AbortError is returned when the wait is aborted early because the condition
// can no longer possibly become true.
type AbortError struct {
	Reason string
}

func (e *AbortError) Error() string {
	return e.Reason
}

// TimeoutError is returned when the condition did not become true before the
// timeout elapsed.
type TimeoutError struct {
	Timeout time.Duration
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("condition not satisfied after %v", e.Timeout)
}

// Poll polls cond every interval until it returns true, until abort reports
// the wait is pointless, or until timeout or ctx cancellation. It returns nil,
// an *AbortError, a *TimeoutError, or ctx.Err().
func Poll(ctx context.Context, timeout, interval time.Duration, cond Condition, abort Abort) error {
	// Immediate check before any sleep or timer
	if abort != nil {
		if aborted, reason := abort(); aborted {
			return &AbortError{Reason: reason}
		}
	}
	if cond() {
		return nil
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if abort != nil {
				if aborted, reason := abort(); aborted {
					return &AbortError{Reason: reason}
				}
			}
			return ctx.Err()
		case <-timer.C:
			if abort != nil {
				if aborted, reason := abort(); aborted {
					return &AbortError{Reason: reason}
				}
			}
			return &TimeoutError{Timeout: timeout}
		case <-ticker.C:
			if abort != nil {
				if aborted, reason := abort(); aborted {
					return &AbortError{Reason: reason}
				}
			}
			if cond() {
				return nil
			}
		}
	}
}
