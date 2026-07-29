package servicehealth_test

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/machine/servicehealth"
)

// This file measures one machine's monitor at the service count and interval
// the platform commits to supporting. The numbers are the commitment; the site
// side of the same envelope is measured in internal/site/healthfabric.
const (
	// loadServices is the maximum services one machine hosts.
	loadServices = 16
	// loadInterval is the shortest probe interval a service may be authored with.
	loadInterval = time.Second
	// loadTimeout is the longest timeout one attempt may be given.
	loadTimeout = 900 * time.Millisecond
	// loadRuns for long enough that every worker has come out of its startup
	// jitter — which is up to one whole interval — and probed at least twice.
	loadRun = 3 * loadInterval
	// loadShutdownBound is how long stopping a fully loaded monitor may take. It
	// is one timeout and a margin: Stop waits out the attempt each worker is in,
	// and an attempt cannot outlast its own timeout.
	loadShutdownBound = loadTimeout + 500*time.Millisecond
)

// TestMonitorSustainsTheSupportedServiceCount runs a machine's whole authored
// load against a real listener, on the real clock.
//
// It is the one test in this package that waits rather than rendezvouses, and
// it has to be: what is being measured is that sixteen workers at the minimum
// interval keep to their schedule and stop promptly, and a controlled clock
// would measure the test's own bookkeeping instead.
//
// Three claims are checked. Every service is probed — no worker starves behind
// the others. The rate stays inside what the interval allows — nothing spins.
// And a fully loaded monitor stops inside one timeout, which is what makes a
// deployment's shutdown bounded rather than hopeful.
func TestMonitorSustainsTheSupportedServiceCount(t *testing.T) {
	if testing.Short() {
		t.Skip("probes a real listener for several seconds")
	}

	var served atomic.Int64
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(service.Close)

	sink := newCountingSink()
	targets := make([]servicehealth.Target, 0, loadServices)
	for i := range loadServices {
		targets = append(targets, servicehealth.Target{
			Service:  fmt.Sprintf("service-%02d", i),
			Role:     "master",
			URL:      service.URL + "/health",
			Interval: loadInterval,
			Timeout:  loadTimeout,
			Retries:  3,
		})
	}

	startedAt := time.Now()
	monitor, err := servicehealth.Start(t.Context(), servicehealth.Deps{
		Prober:   servicehealth.NewHTTPProber(),
		Sink:     sink,
		Clock:    servicehealth.SystemClock{},
		Observer: "primary",
	}, targets)
	require.NoError(t, err)

	time.Sleep(loadRun)

	stopStart := time.Now()
	monitor.Stop()
	shutdown := time.Since(stopStart)
	elapsed := time.Since(startedAt)

	byService, total, healthy := sink.tally()

	// Every service was asked about. A worker still inside its jitter after three
	// intervals would be one the scheduler lost, since the jitter is bounded by a
	// single interval.
	require.Len(t, byService, loadServices, "every service produced observations")
	for service, attempts := range byService {
		require.GreaterOrEqualf(t, attempts, 2, "%s was probed more than once", service)
	}
	require.Equal(t, total, healthy, "a listener that answered 200 leaves nothing unhealthy")

	// The rate is what the interval allows and no more. The upper bound is one
	// attempt per interval per service plus the immediate first one each worker
	// makes when it comes out of its jitter; anything above that is a worker that
	// stopped waiting.
	ceiling := loadServices * (int(loadRun/loadInterval) + 1)
	require.LessOrEqualf(t, total, ceiling,
		"%d attempts in %s is above what %d services at %s allow", total, elapsed, loadServices, loadInterval)
	require.Equal(t, int64(total), served.Load(), "every observation is one request the service actually served")

	require.Lessf(t, shutdown, loadShutdownBound,
		"a fully loaded monitor stopped in %s, past the %s a deployment budgets", shutdown, loadShutdownBound)

	t.Logf("supported machine load: %d services at %s, timeout %s", loadServices, loadInterval, loadTimeout)
	t.Logf("measured over %s: %d attempts, %d requests served, %.1f attempts/s",
		elapsed.Round(time.Millisecond), total, served.Load(), float64(total)/elapsed.Seconds())
	t.Logf("stopping a fully loaded monitor took %s, bound %s", shutdown.Round(time.Microsecond), loadShutdownBound)
}

// countingSink tallies observations instead of holding them. At this rate the
// recording sink's channel would be the thing under test.
type countingSink struct {
	mu        sync.Mutex
	byService map[string]int
	total     int
	healthy   int
}

func newCountingSink() *countingSink {
	return &countingSink{byService: make(map[string]int, loadServices)}
}

func (s *countingSink) Observed(_ context.Context, observation servicehealth.Observation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byService[observation.Service]++
	s.total++
	if observation.Status == servicehealth.StatusHealthy {
		s.healthy++
	}
}

func (s *countingSink) tally() (byService map[string]int, total, healthy int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.byService), s.total, s.healthy
}
