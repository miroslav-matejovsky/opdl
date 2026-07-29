package servicehealth_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/machine/servicehealth"
)

// These tests drive real workers on real goroutines, and none of them sleeps.
// The clock hands out wait channels the test releases by hand, and the sink is
// a channel the test receives from, so every step is a rendezvous rather than a
// guess about timing. The only durations here are the guards that turn a
// deadlock into a named failure.

// settle bounds how long a test waits for something it expects to arrive
// promptly. It is never the happy path: reaching it means a worker did not do
// what the test released it to do.
const settle = 5 * time.Second

var errRefused = errors.New("connection refused")

// testClock is the passage of time under the test's control.
//
// Now moves only when a probe says it did, so an observation's latency is
// exactly what the scripted attempt cost. After registers a wait the test
// releases explicitly, which is what makes "the worker probed again" an event a
// test causes rather than one it waits out.
type testClock struct {
	mu    sync.Mutex
	now   time.Time
	waits chan chan time.Time
}

func newTestClock() *testClock {
	return &testClock{
		now:   time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		waits: make(chan chan time.Time, 64),
	}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *testClock) After(time.Duration) <-chan time.Time {
	wait := make(chan time.Time, 1)
	c.waits <- wait
	return wait
}

// releaseInterval lets one waiting worker begin its next attempt.
func (c *testClock) releaseInterval(t *testing.T) {
	t.Helper()
	select {
	case wait := <-c.waits:
		wait <- c.Now()
	case <-time.After(settle):
		require.FailNow(t, "no worker was waiting out an interval")
	}
}

// scriptedProber answers whatever the test last told it to, and advances the
// clock by what the attempt is meant to have cost.
type scriptedProber struct {
	mu      sync.Mutex
	clock   *testClock
	err     error
	latency time.Duration
	calls   int
	// started, when set, is signaled as an attempt begins and then holds that
	// attempt until release is closed or the attempt's context ends. It is how a
	// test gets a worker to be inside a probe at a chosen moment. Leaving release
	// nil holds the attempt until cancellation, which is what a probe interrupted
	// by shutdown does.
	started chan struct{}
	release chan struct{}
}

func (p *scriptedProber) Probe(ctx context.Context, _ servicehealth.Target) error {
	p.mu.Lock()
	p.calls++
	err, latency := p.err, p.latency
	started, release := p.started, p.release
	p.mu.Unlock()

	if started != nil {
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	p.clock.advance(latency)
	return err
}

func (p *scriptedProber) answer(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.err = err
}

func (p *scriptedProber) attempts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// recordingSink is a channel of observations, so a test receives the next one
// rather than polling for it.
type recordingSink struct {
	observed chan servicehealth.Observation
}

func newRecordingSink() *recordingSink {
	return &recordingSink{observed: make(chan servicehealth.Observation, 64)}
}

func (s *recordingSink) Observed(_ context.Context, observation servicehealth.Observation) {
	s.observed <- observation
}

// next returns the next observation, failing the test rather than blocking
// forever when none arrives.
func (s *recordingSink) next(t *testing.T) servicehealth.Observation {
	t.Helper()
	select {
	case observation := <-s.observed:
		return observation
	case <-time.After(settle):
		require.FailNow(t, "no observation was reported")
		return servicehealth.Observation{}
	}
}

// nextByService receives count observations and indexes them by service.
//
// Two workers report concurrently and the order they do it in is not something
// this package promises, so a test with two targets collects both and looks
// each up rather than expecting one first.
func (s *recordingSink) nextByService(t *testing.T, count int) map[string]servicehealth.Observation {
	t.Helper()
	byService := make(map[string]servicehealth.Observation, count)
	for range count {
		observation := s.next(t)
		byService[observation.Service] = observation
	}
	require.Len(t, byService, count, "each observation was about a different service")
	return byService
}

func testTarget(service string, retries int) servicehealth.Target {
	return servicehealth.Target{
		Service:  service,
		Role:     "master",
		URL:      "http://10.0.1.10:9101/health",
		Interval: 10 * time.Second,
		Timeout:  2 * time.Second,
		Retries:  retries,
	}
}

// start composes a monitor on the test's seams and stops it when the test ends.
func start(ctx context.Context, t *testing.T, prober *scriptedProber, clock *testClock, sink *recordingSink, targets ...servicehealth.Target) *servicehealth.Monitor {
	t.Helper()
	monitor, err := servicehealth.Start(ctx, servicehealth.Deps{
		Prober: prober,
		Sink:   sink,
		Clock:  clock,
	}, targets)
	require.NoError(t, err)
	t.Cleanup(monitor.Stop)
	return monitor
}

// TestMonitorProbesImmediatelyAndReportsEveryAttempt checks a service is asked
// about from the moment the process starts, and that an attempt which changed
// nothing is still reported.
//
// Reporting unchanged states is what repairs a lost message and what tells a
// subscriber that started late where a service stands. Distribution is
// at-most-once with no replay, so transitions alone would leave both unfixable.
func TestMonitorProbesImmediatelyAndReportsEveryAttempt(t *testing.T) {
	clock := newTestClock()
	prober := &scriptedProber{clock: clock, latency: 4 * time.Millisecond}
	sink := newRecordingSink()
	start(t.Context(), t, prober, clock, sink, testTarget("alarm-service", 3))

	// No interval has been released, so this attempt happened because the worker
	// starts by probing rather than by waiting.
	first := sink.next(t)
	require.Equal(t, "alarm-service", first.Service)
	require.Equal(t, "master", first.Role)
	require.Equal(t, servicehealth.StatusHealthy, first.Status)
	require.Zero(t, first.PendingFailures)
	require.Empty(t, first.Error)
	require.True(t, first.Succeeded())
	require.Equal(t, 4*time.Millisecond, first.Latency, "latency is what the attempt cost on the clock")

	clock.releaseInterval(t)
	second := sink.next(t)
	require.Equal(t, servicehealth.StatusHealthy, second.Status,
		"an attempt that changed nothing is still reported")
	require.Equal(t, first.CheckedAt.Add(4*time.Millisecond), second.CheckedAt,
		"the second attempt began where the first one left the clock")
}

// TestMonitorReportsThresholdCrossingAndRecovery walks a service down and back
// up through the worker rather than through the tracker, so the wiring between
// the two is covered as well as the policy.
func TestMonitorReportsThresholdCrossingAndRecovery(t *testing.T) {
	clock := newTestClock()
	prober := &scriptedProber{clock: clock, latency: time.Millisecond, err: errRefused}
	sink := newRecordingSink()
	start(t.Context(), t, prober, clock, sink, testTarget("alarm-service", 2))

	first := sink.next(t)
	require.Equal(t, servicehealth.StatusUnknown, first.Status,
		"one failure below the threshold has decided nothing")
	require.Equal(t, 1, first.PendingFailures)
	require.Contains(t, first.Error, "connection refused")
	require.False(t, first.Succeeded())
	require.Equal(t, time.Millisecond, first.Latency,
		"a failed attempt's latency is how long the service took to refuse")

	clock.releaseInterval(t)
	second := sink.next(t)
	require.Equal(t, servicehealth.StatusUnhealthy, second.Status, "the second consecutive failure reaches retries")
	require.Equal(t, 2, second.PendingFailures)

	prober.answer(nil)
	clock.releaseInterval(t)
	recovered := sink.next(t)
	require.Equal(t, servicehealth.StatusHealthy, recovered.Status, "a service that answered is answering")
	require.Zero(t, recovered.PendingFailures)
	require.Empty(t, recovered.Error)
}

// TestMonitorTreatsATimeoutAsAFailedAttempt checks an attempt the service did
// not answer in time counts against it.
//
// A timeout is the service failing to answer within the terms its own
// deployment authored, which is exactly what a failed attempt is. It is the one
// failure that looks like a cancellation, and the test below is its opposite
// number.
func TestMonitorTreatsATimeoutAsAFailedAttempt(t *testing.T) {
	clock := newTestClock()
	prober := &scriptedProber{clock: clock, err: context.DeadlineExceeded}
	sink := newRecordingSink()
	start(t.Context(), t, prober, clock, sink, testTarget("alarm-service", 1))

	observation := sink.next(t)
	require.Equal(t, servicehealth.StatusUnhealthy, observation.Status)
	require.Equal(t, 1, observation.PendingFailures)
	require.Contains(t, observation.Error, context.DeadlineExceeded.Error())
}

// TestMonitorDoesNotRecordAnAttemptCutShortByShutdown checks a probe interrupted
// by the process stopping is not folded in as a failure.
//
// The service was never given its timeout to answer in, so recording one would
// end every deployment's health record with an outage that did not happen. The
// prober is held inside an attempt, the monitor is stopped underneath it, and
// nothing may be reported afterwards.
func TestMonitorDoesNotRecordAnAttemptCutShortByShutdown(t *testing.T) {
	clock := newTestClock()
	// release stays nil, so the attempt is held until its context ends rather
	// than until the test lets it finish. That is what a probe interrupted by
	// shutdown looks like.
	prober := &scriptedProber{clock: clock, started: make(chan struct{}, 1)}
	sink := newRecordingSink()
	monitor, err := servicehealth.Start(t.Context(), servicehealth.Deps{
		Prober: prober,
		Sink:   sink,
		Clock:  clock,
	}, []servicehealth.Target{testTarget("alarm-service", 3)})
	require.NoError(t, err)

	// Wait until the worker is inside a probe, then stop it there.
	select {
	case <-prober.started:
	case <-time.After(settle):
		require.FailNow(t, "the worker never began an attempt")
	}

	// Stop cancels the attempt's context, and the held probe returns ctx.Err(),
	// which is what a real prober does when the process is stopping.
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		monitor.Stop()
	}()

	select {
	case <-stopped:
	case <-time.After(settle):
		require.FailNow(t, "Stop did not return; a worker was left running")
	}

	select {
	case observation := <-sink.observed:
		require.FailNowf(t, "a canceled attempt was reported",
			"shutdown is not a target failure, but %+v was recorded", observation)
	default:
	}
}

// TestMonitorRunsOneIndependentWorkerPerTarget checks two services are probed on
// their own terms and tracked apart.
//
// One target failing must neither delay nor colour another. They share a prober
// and a sink, which is what a running process gives them, so the separation
// being tested is the worker's rather than the plumbing's.
func TestMonitorRunsOneIndependentWorkerPerTarget(t *testing.T) {
	clock := newTestClock()
	prober := &scriptedProber{clock: clock, err: errRefused}
	sink := newRecordingSink()
	start(t.Context(), t, prober, clock, sink,
		testTarget("alarm-service", 1),
		testTarget("reporting-service", 1),
	)

	first := sink.nextByService(t, 2)
	require.Equal(t, servicehealth.StatusUnhealthy, first["alarm-service"].Status)
	require.Equal(t, servicehealth.StatusUnhealthy, first["reporting-service"].Status)

	// Only one of the two workers is released, so only one probes again. The
	// other stays where it was, which is what independent scheduling means.
	prober.answer(nil)
	clock.releaseInterval(t)
	next := sink.next(t)
	require.Equal(t, servicehealth.StatusHealthy, next.Status)

	select {
	case extra := <-sink.observed:
		require.FailNowf(t, "a worker that was not released still probed",
			"unexpected observation %+v", extra)
	default:
	}
}

// TestStopWaitsForWorkersAndIsIdempotent checks the guarantee the composition
// root depends on: when Stop returns, nothing is still probing.
//
// A leaked worker would hang Stop rather than fail an assertion, which is the
// strongest form this can take. Stopping twice is a no-op because a composition
// root that stops on both a signal path and a deferred cleanup is doing the
// right thing.
func TestStopWaitsForWorkersAndIsIdempotent(t *testing.T) {
	clock := newTestClock()
	prober := &scriptedProber{clock: clock}
	sink := newRecordingSink()
	monitor, err := servicehealth.Start(t.Context(), servicehealth.Deps{
		Prober: prober,
		Sink:   sink,
		Clock:  clock,
	}, []servicehealth.Target{testTarget("alarm-service", 3)})
	require.NoError(t, err)

	sink.next(t)
	monitor.Stop()

	after := prober.attempts()
	monitor.Stop()
	require.Equal(t, after, prober.attempts(), "no attempt is made once the monitor has stopped")

	select {
	case observation := <-sink.observed:
		require.FailNowf(t, "an observation arrived after Stop returned",
			"unexpected observation %+v", observation)
	default:
	}
}

// TestMonitorStopsWhenItsContextEnds checks the workers hold a context derived
// from the caller's, so a process that is shutting down ends probing without
// being told twice.
func TestMonitorStopsWhenItsContextEnds(t *testing.T) {
	clock := newTestClock()
	prober := &scriptedProber{clock: clock}
	sink := newRecordingSink()
	ctx, cancel := context.WithCancel(t.Context())
	monitor := start(ctx, t, prober, clock, sink, testTarget("alarm-service", 3))

	sink.next(t)
	cancel()

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		monitor.Stop()
	}()
	select {
	case <-stopped:
	case <-time.After(settle):
		require.FailNow(t, "a canceled context did not end the workers")
	}
}

// TestStartRejectsAnUnusableComposition checks a monitor that could not do its
// job does not start.
//
// The descriptor was validated when it decoded, so none of this second-guesses
// the author. What it guards is the adapter between the descriptor and this
// package: a target assembled with a zero interval or an unresolved URL would
// produce a worker that spins or one that probes nothing, and either is quieter
// than refusing to start.
func TestStartRejectsAnUnusableComposition(t *testing.T) {
	valid := testTarget("alarm-service", 3)
	deps := servicehealth.Deps{Prober: &scriptedProber{clock: newTestClock()}, Sink: newRecordingSink(), Clock: newTestClock()}

	tests := map[string]struct {
		deps    servicehealth.Deps
		targets []servicehealth.Target
		errText string
	}{
		"no prober": {
			deps:    servicehealth.Deps{Sink: deps.Sink, Clock: deps.Clock},
			targets: []servicehealth.Target{valid},
			errText: "prober is required",
		},
		"no sink": {
			deps:    servicehealth.Deps{Prober: deps.Prober, Clock: deps.Clock},
			targets: []servicehealth.Target{valid},
			errText: "sink is required",
		},
		"no clock": {
			deps:    servicehealth.Deps{Prober: deps.Prober, Sink: deps.Sink},
			targets: []servicehealth.Target{valid},
			errText: "clock is required",
		},
		"target with no service": {
			deps:    deps,
			targets: []servicehealth.Target{func() servicehealth.Target { t := valid; t.Service = " "; return t }()},
			errText: "service is required",
		},
		"target with a relative url": {
			deps:    deps,
			targets: []servicehealth.Target{func() servicehealth.Target { t := valid; t.URL = "/health"; return t }()},
			errText: "has no scheme",
		},
		"target with no host": {
			deps:    deps,
			targets: []servicehealth.Target{func() servicehealth.Target { t := valid; t.URL = "http:///health"; return t }()},
			errText: "has no host",
		},
		"target with no interval": {
			deps:    deps,
			targets: []servicehealth.Target{func() servicehealth.Target { t := valid; t.Interval = 0; return t }()},
			errText: "interval 0s must be positive",
		},
		"target with no timeout": {
			deps:    deps,
			targets: []servicehealth.Target{func() servicehealth.Target { t := valid; t.Timeout = 0; return t }()},
			errText: "timeout 0s must be positive",
		},
		"target whose timeout outlasts its interval": {
			deps:    deps,
			targets: []servicehealth.Target{func() servicehealth.Target { t := valid; t.Timeout = t.Interval; return t }()},
			errText: "must be shorter than interval",
		},
		"target with no retries": {
			deps:    deps,
			targets: []servicehealth.Target{func() servicehealth.Target { t := valid; t.Retries = 0; return t }()},
			errText: "retries must be at least 1",
		},
		"one service targeted twice": {
			deps:    deps,
			targets: []servicehealth.Target{valid, valid},
			errText: "is targeted more than once",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			monitor, err := servicehealth.Start(t.Context(), test.deps, test.targets)
			require.ErrorContains(t, err, test.errText)
			require.Nil(t, monitor, "a monitor that cannot run is not half-started")
		})
	}
}

// TestStartWithNoTargetsRuns checks a machine whose services are all absent from
// the descriptor is not an error here. Whether a machine must host a service is
// the descriptor's rule, and it enforces it; a monitor with nothing to watch
// simply watches nothing.
func TestStartWithNoTargetsRuns(t *testing.T) {
	clock := newTestClock()
	monitor, err := servicehealth.Start(t.Context(), servicehealth.Deps{
		Prober: &scriptedProber{clock: clock},
		Sink:   newRecordingSink(),
		Clock:  clock,
	}, nil)
	require.NoError(t, err)
	monitor.Stop()
}
