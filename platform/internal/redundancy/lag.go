package redundancy

import "time"

// LagState tracks how long a slot's projection has continuously been behind the
// journal. The runtime updates it from one monitoring loop, so it is not safe for
// concurrent use.
//
// Lag is a duration, not an event count. A projection that is a few events behind
// for a moment is healthy; a projection that stays behind for a while is not, and
// only the duration tells the two apart.
type LagState struct {
	since time.Time // zero while the projection is caught up
}

// Observe records whether the projection is behind at now and returns the current
// lag: zero when caught up, otherwise how long it has continuously been behind.
// Catching up resets the measurement, so a brief lag that recovers does not
// accumulate against a later one.
func (l *LagState) Observe(behind bool, now time.Time) time.Duration {
	if !behind {
		l.since = time.Time{}
		return 0
	}
	if l.since.IsZero() {
		l.since = now
	}
	return now.Sub(l.since)
}

// Exceeds reports whether lag is beyond bound. A non-positive bound disables the
// check, so lag never counts against a slot whose deployment has not set one.
func Exceeds(lag, bound time.Duration) bool {
	return bound > 0 && lag > bound
}
