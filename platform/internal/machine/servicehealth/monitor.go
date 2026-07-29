package servicehealth

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Deps are the seams a monitor runs on: what performs an attempt, what receives
// the result, and what time it is.
//
// All three are required. A monitor composed without one would be a monitor
// that cannot probe, cannot report, or cannot schedule, and defaulting any of
// them here would put a decision the composition root should be making inside a
// package that knows nothing about the deployment.
type Deps struct {
	// Prober performs one attempt. A running process passes NewHTTPProber.
	Prober Prober
	// Sink receives every completed attempt, on the worker's own goroutine.
	Sink Sink
	// Clock stamps observations and times the wait between attempts. A running
	// process passes SystemClock{}.
	Clock Clock
	// Observer is who is doing the probing: the fixed platform instance role,
	// "primary" or "standby". It is what makes a machine's two instances start
	// their workers at different points in the interval instead of together; see
	// jitterFor.
	//
	// It is required rather than defaulted for the same reason the seams above
	// are. A monitor that invented an observer identity would decide, inside a
	// package that knows nothing about the deployment, which observers collide.
	Observer string
}

func (d Deps) validate() error {
	switch {
	case d.Prober == nil:
		return errors.New("prober is required")
	case d.Sink == nil:
		return errors.New("sink is required")
	case d.Clock == nil:
		return errors.New("clock is required")
	case strings.TrimSpace(d.Observer) == "":
		return errors.New("observer is required")
	}
	return nil
}

// Monitor is one worker per local service, running until it is stopped.
//
// Its lifetime is the process's, not an activation's: it is started once, by
// the composition root, and keeps probing across every ownership change the
// process sees. Nothing about it consults which instance is Active, because a
// service on this machine is this machine's to watch either way.
type Monitor struct {
	stop    context.CancelFunc
	workers sync.WaitGroup
	// stopped guards Stop against being called twice. A composition root that
	// stops on both a signal path and a deferred cleanup is doing the right
	// thing, and the second call is a no-op rather than a panic on a closed
	// context.
	stopped sync.Once
}

// Start begins probing every target and returns once the workers are running.
//
// It returns an error rather than starting a partial monitor when a target or a
// seam is unusable, so a deployment whose health policy cannot be run fails
// where the process starts rather than reporting a service Unknown forever.
//
// The returned Monitor must be stopped. Its workers hold a context derived from
// ctx, so a canceled ctx ends them too; Stop is what waits for them to finish
// the attempt they are in.
func Start(ctx context.Context, deps Deps, targets []Target) (*Monitor, error) {
	if err := deps.validate(); err != nil {
		return nil, fmt.Errorf("servicehealth: %w", err)
	}
	named := make(map[string]bool, len(targets))
	for _, target := range targets {
		if err := target.validate(); err != nil {
			return nil, fmt.Errorf("servicehealth: %w", err)
		}
		if named[target.Service] {
			return nil, fmt.Errorf("servicehealth: service %q is targeted more than once", target.Service)
		}
		named[target.Service] = true
	}

	// The workers' context is derived from ctx rather than detached from it, so
	// a process that is stopping ends probing without waiting to be told twice.
	workerCtx, stop := context.WithCancel(ctx)
	monitor := &Monitor{stop: stop}
	for _, target := range targets {
		worker := &worker{
			target: target,
			deps:   deps,
			track:  newTracker(target.Retries),
			jitter: jitterFor(deps.Observer, target.Service, target.Interval),
		}
		monitor.workers.Go(func() { worker.run(workerCtx) })
	}
	slog.Info("service health monitoring started", "targets", len(targets), "observer", deps.Observer)
	return monitor, nil
}

// jitterFor is how long this observer's worker for this service waits before
// its first attempt, somewhere in [0, interval).
//
// It exists because both of a machine's instances probe every service on it, and
// both start their workers the moment their process does. Without this they
// probe together: a machine rebooting starts its two instances within the same
// second, every service on it is asked twice at once, and because each worker
// then waits a fixed interval the pair stays in step for as long as both run. A
// service authored with sixteen siblings receives all thirty-two of those
// requests in the same instant, forever.
//
// It is derived rather than random, and that is the part worth keeping. A
// restarted instance resumes the phase it had before, so a restart cannot move
// it onto its peer; two processes with the same observer identity and the same
// service agree without coordinating; and a failing deployment can be reasoned
// about from the descriptor rather than from what a seed happened to produce.
//
// The observer and the service are separated by a byte that can appear in
// neither, so ("primary", "ab") and ("primar", "yab") do not hash alike.
func jitterFor(observer, service string, interval time.Duration) time.Duration {
	if interval <= 0 {
		return 0
	}
	digest := fnv.New64a()
	_, _ = digest.Write([]byte(observer))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(service))
	return time.Duration(digest.Sum64() % uint64(interval))
}

// Stop ends every worker and waits for the attempt each is in to finish.
//
// Waiting is the point. A worker that is mid-probe holds a connection and is
// about to call the sink, and returning before it has finished would let the
// composition root close what it is still using. It is safe to call more than
// once.
func (m *Monitor) Stop() {
	m.stopped.Do(func() {
		m.stop()
		m.workers.Wait()
		slog.Info("service health monitoring stopped")
	})
}

// worker probes one target for as long as its context lives.
type worker struct {
	target Target
	deps   Deps
	track  *tracker
	// jitter is how long this worker waits before its first attempt. It is fixed
	// for the process's life and derived from who is observing what; see
	// jitterFor.
	jitter time.Duration
}

// run waits out its startup jitter, then probes, reports, waits one interval,
// and repeats.
//
// The interval wait comes after the attempt rather than on a fixed schedule, so
// two attempts against one target never overlap: a probe that took most of its
// timeout delays the next one instead of running beside it.
//
// The jitter is what the first attempt waits instead of happening at once. It
// costs a service its first status for less than one interval — the view calls
// it Unknown until then, which is what it is — and buys a machine's two
// instances, and a machine's several services, phases that do not coincide.
func (w *worker) run(ctx context.Context) {
	if w.jitter > 0 {
		select {
		case <-w.deps.Clock.After(w.jitter):
		case <-ctx.Done():
			return
		}
	}
	for {
		w.attempt(ctx)
		select {
		case <-w.deps.Clock.After(w.target.Interval):
		case <-ctx.Done():
			return
		}
	}
}

// attempt runs one probe and reports what it found.
//
// Nothing is recorded when the process is stopping. A probe cut short by
// shutdown says nothing about the service — it was never given its timeout to
// answer in — and folding it in as a failure would end every deployment's
// health record with an outage that did not happen. The parent context is what
// is checked rather than the probe's error, because a timeout and a shutdown
// both surface as a canceled context and only one of them is the service's
// fault.
func (w *worker) attempt(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	startedAt := w.deps.Clock.Now()

	probeCtx, cancel := context.WithTimeout(ctx, w.target.Timeout)
	err := w.deps.Prober.Probe(probeCtx, w.target)
	cancel()

	if ctx.Err() != nil {
		return
	}

	status, pending := w.track.record(err)
	observation := Observation{
		Service:         w.target.Service,
		Role:            w.target.Role,
		Status:          status,
		CheckedAt:       startedAt,
		Latency:         w.deps.Clock.Now().Sub(startedAt),
		PendingFailures: pending,
	}
	if err != nil {
		observation.Error = err.Error()
	}
	// Reported on this worker's own goroutine, in order, after every attempt —
	// including one that changed nothing. Distribution is at-most-once with no
	// replay, so an unchanged snapshot is what repairs a lost message and what
	// tells a late subscriber where this service stands.
	w.deps.Sink.Observed(ctx, observation)
}
