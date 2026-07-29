package servicehealth

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Status is what a probing instance has stably determined about one service.
//
// There are three because there are three answers, and the third is not a
// shade of the other two: a service nobody has successfully asked yet is not
// failing, and reporting it as either Healthy or Unhealthy would invent a
// result. Degraded is deliberately absent — it describes observers disagreeing
// about a service, which is a reduction across observers and not something one
// of them can see.
type Status string

const (
	// StatusUnknown is a target no attempt has yet resolved: the worker has not
	// run, or every attempt so far failed without reaching the retry threshold
	// that would make it Unhealthy.
	StatusUnknown Status = "unknown"
	// StatusHealthy is a target whose last attempt succeeded.
	StatusHealthy Status = "healthy"
	// StatusUnhealthy is a target that has failed at least its authored retries
	// consecutively.
	StatusUnhealthy Status = "unhealthy"
)

// Target is one service on this machine and the terms it is probed on.
//
// It carries a resolved request URL rather than the parts of one. Where a probe
// connects is the machine's own ip joined to the authored port, which the
// composition root knows and this package does not; handing it a URL keeps the
// descriptor's shape out of the worker and keeps the address a probe uses
// decided in exactly one place.
type Target struct {
	// Service is the service name, unique on this machine. It is how an
	// observation is keyed and how a reader tells two targets apart.
	Service string
	// Role is the part this machine's copy plays: "master" or "slave". The
	// platform probes both the same way; this travels with the result so a reader
	// knows which copy answered.
	Role string
	// URL is the absolute request URL one attempt fetches.
	URL string
	// Interval is how long the worker waits after an attempt completes before
	// beginning the next one.
	Interval time.Duration
	// Timeout bounds one attempt. It is shorter than Interval, so an attempt that
	// runs to its limit still finishes before the next is due.
	Timeout time.Duration
	// Retries is how many consecutive failures make a target Unhealthy. It is at
	// least 1: a service that tolerates no failure is still failed by the first.
	Retries int
}

// validate checks a target is one a worker can actually run.
//
// The descriptor was validated when it decoded, so this is not a second guess
// at the author. It is a guard on the adapter between the two: a target
// assembled with a zero interval or an unresolved URL would produce a worker
// that spins or one that probes nothing, and both are quieter failures than
// refusing to start.
func (t Target) validate() error {
	if strings.TrimSpace(t.Service) == "" {
		return fmt.Errorf("service is required")
	}
	where := fmt.Sprintf("service %q", t.Service)
	parsed, err := url.Parse(t.URL)
	if err != nil {
		return fmt.Errorf("%s: url %q is not a URL: %w", where, t.URL, err)
	}
	switch {
	case parsed.Scheme == "":
		return fmt.Errorf("%s: url %q has no scheme; the composition root resolves an absolute URL", where, t.URL)
	case parsed.Host == "":
		return fmt.Errorf("%s: url %q has no host; a probe connects to the machine's own ip", where, t.URL)
	case t.Interval <= 0:
		return fmt.Errorf("%s: interval %s must be positive", where, t.Interval)
	case t.Timeout <= 0:
		return fmt.Errorf("%s: timeout %s must be positive", where, t.Timeout)
	case t.Timeout >= t.Interval:
		return fmt.Errorf("%s: timeout %s must be shorter than interval %s", where, t.Timeout, t.Interval)
	case t.Retries < 1:
		return fmt.Errorf("%s: retries must be at least 1, got %d", where, t.Retries)
	}
	return nil
}

// Observation is what one completed attempt produced: the target's stable
// status after folding the attempt in, and what the attempt itself cost.
//
// Status is the stable one rather than the attempt's own outcome. A single
// failure below the retry threshold does not change what the platform says
// about a service, and PendingFailures is what shows the difference between a
// service that is fine and one that is on its way to being reported down.
type Observation struct {
	// Service is the target this is about, and Role is the part its copy plays.
	Service string
	Role    string
	// Status is the target's stable status after this attempt.
	Status Status
	// CheckedAt is when the attempt began, from the injected clock.
	CheckedAt time.Time
	// Latency is how long the attempt took, whether it succeeded or failed. A
	// failure's latency is how long the service took to refuse or to time out,
	// which is worth as much to a reader as a success's.
	Latency time.Duration
	// PendingFailures is how many consecutive failures have accumulated. It is
	// zero after a success, and it stops climbing once the target is Unhealthy:
	// past the threshold the status carries the fact and the count adds nothing.
	PendingFailures int
	// Error is why the attempt failed, empty when it succeeded. It is diagnostic
	// text for a person, never something to route or compare on.
	Error string
}

// Succeeded reports whether the attempt behind this observation reached the
// service and got an acceptable answer.
func (o Observation) Succeeded() bool { return o.Error == "" }

// Prober performs one probe attempt against a target.
//
// It returns nil when the service answered acceptably and an error describing
// what went wrong otherwise. Whether an error means the target is down is not
// its decision: it reports one attempt, and the retry policy is applied above
// it.
//
// It must honour ctx. The worker bounds every attempt with the target's
// timeout, and a prober that ignored cancellation would let one slow service
// hold up its own next attempt indefinitely.
type Prober interface {
	Probe(ctx context.Context, target Target) error
}

// Sink receives every completed attempt.
//
// It is called on the worker's own goroutine, in order, once per attempt. An
// implementation that blocks stops that target being probed, so a sink that
// talks to anything slower than memory buffers rather than waits.
//
// It returns nothing. A worker has no useful response to a sink that failed:
// the next attempt produces a fresher observation than the one that was lost,
// and stopping the probe because a report did not go out would turn a reporting
// fault into a monitoring outage.
type Sink interface {
	Observed(ctx context.Context, observation Observation)
}

// Clock is the passage of time a worker sees.
//
// It is injected so the scheduling this package promises can be tested without
// waiting for it. Now stamps an observation; After is how a worker waits out an
// interval.
type Clock interface {
	// Now is the current time.
	Now() time.Time
	// After returns a channel that receives once, after d has passed.
	After(d time.Duration) <-chan time.Time
}

// SystemClock is the real clock, and the one a running process uses.
type SystemClock struct{}

// Now returns the current time.
func (SystemClock) Now() time.Time { return time.Now() }

// After returns a channel that receives after d.
func (SystemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
