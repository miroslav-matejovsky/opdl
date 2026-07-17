package nats

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/platform/internal/eventfabric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// The tests in this file run a real embedded NATS server with JetStream. They
// bind sockets and write files, so they are skipped under -short and run in the
// integration gate, matching the olric adapter. Reusable server setup lives here
// rather than in a separate package, as the plan requires.

var testDescriptor = deployment.Descriptor{
	Platform: "opdl", Project: "customer-a", Environment: "production",
	Site: "north", Machine: "node", Role: "all-in-one", IP: "127.0.0.1",
}

// open starts a fabric for cfg and closes it when the test ends. It skips the
// test in -short mode, where sockets are not bound.
func open(t *testing.T, cfg Config) *Fabric {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping NATS integration test in -short mode")
	}
	f, err := Open(context.Background(), testDescriptor, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close(context.Background()) })
	return f
}

// testConfig is a single-node loopback storage configuration on free ports with
// a fresh data directory.
func testConfig(t *testing.T) Config {
	t.Helper()
	return configForDir(t, filepath.Join(t.TempDir(), "nats"))
}

// configForDir is a single-node loopback storage configuration whose journal
// lives in dir, so a restart can reopen the same journal.
func configForDir(t *testing.T, dir string) Config {
	t.Helper()
	addrs := freeAddrs(t, 3)
	return Config{
		ServerName:      "node",
		ClusterName:     "test",
		ClientAddress:   addrs[0],
		ClusterAddress:  addrs[1],
		MonitorAddress:  addrs[2],
		Servers:         []string{addrs[0]},
		HostsStorage:    true,
		DataDir:         dir,
		Replicas:        1,
		MaxBytes:        DefaultMaxBytes,
		MaxMessageBytes: DefaultMaxMessageBytes,
		AckWait:         2 * time.Second,
		MaxDeliver:      3,
		StartupTimeout:  20 * time.Second,
		CatchUpTimeout:  20 * time.Second,
		ShutdownTimeout: 10 * time.Second,
	}
}

// freeAddrs reserves n distinct loopback addresses, then releases them so a
// server can bind them. Holding every listener at once guarantees the ports are
// distinct.
func freeAddrs(t *testing.T, n int) []string {
	t.Helper()
	var config net.ListenConfig
	listeners := make([]net.Listener, n)
	addrs := make([]string, n)
	for i := range n {
		listener, err := config.Listen(context.Background(), "tcp", "127.0.0.1:0")
		require.NoError(t, err)
		listeners[i] = listener
		addrs[i] = listener.Addr().String()
	}
	for _, listener := range listeners {
		require.NoError(t, listener.Close())
	}
	return addrs
}

// probeRoute is the site route for the probe event every handler test consumes.
func probeRoute(t *testing.T) eventfabric.Route {
	t.Helper()
	route, err := eventfabric.NewRoute(
		eventfabric.NewSiteScope(testDescriptor.Project, testDescriptor.Environment, testDescriptor.Site),
		probe{}.EventType(),
	)
	require.NoError(t, err)
	return route
}

// probe is a plain domain event. The fabric deduplicates it by its unique
// envelope id, so each publish is a distinct journal entry.
type probe struct {
	Fact string `json:"fact"`
}

func (probe) EventType() events.Type { return "platform.probe.happened" }
func (probe) Source() string         { return "probe" }

// keyedProbe carries a stable publication identity, so republishing it inside
// the deduplication window collapses onto one journal entry.
type keyedProbe struct {
	Key string `json:"key"`
}

func (keyedProbe) EventType() events.Type { return "platform.probe.keyed" }
func (keyedProbe) Source() string         { return "probe" }
func (k keyedProbe) DedupID() string      { return k.Key }

// badEvent declares an event type that is not routable.
type badEvent struct{}

func (badEvent) EventType() events.Type { return "not.routable.extra.tokens" }
func (badEvent) Source() string         { return "probe" }

// recorder is a projector that records every delivery, and can be told to fail.
type recorder struct {
	mu   sync.Mutex
	got  []eventfabric.Delivery
	fail error
}

func (r *recorder) Apply(_ context.Context, delivery eventfabric.Delivery) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return r.fail
	}
	r.got = append(r.got, delivery)
	return nil
}

func (r *recorder) count(eventType events.Type) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	total := 0
	for _, delivery := range r.got {
		if delivery.Record.Type == eventType {
			total++
		}
	}
	return total
}

func (r *recorder) first(eventType events.Type) (eventfabric.Delivery, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, delivery := range r.got {
		if delivery.Record.Type == eventType {
			return delivery, true
		}
	}
	return eventfabric.Delivery{}, false
}

// recordHandler records handled deliveries. It can fail the first failUntil
// attempts of each sequence, or fail every attempt, so redelivery and exhaustion
// are exercised.
type recordHandler struct {
	name       string
	routes     []eventfabric.Route
	failUntil  int
	alwaysFail bool

	mu       sync.Mutex
	handled  []eventfabric.Delivery
	attempts map[uint64]int
}

func (h *recordHandler) Name() string                { return h.name }
func (h *recordHandler) Routes() []eventfabric.Route { return h.routes }

func (h *recordHandler) Handle(_ context.Context, delivery eventfabric.Delivery) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.attempts == nil {
		h.attempts = make(map[uint64]int)
	}
	h.attempts[delivery.Sequence]++
	if h.alwaysFail {
		return errors.New("handler always fails")
	}
	if h.attempts[delivery.Sequence] <= h.failUntil {
		return errors.New("transient handler failure")
	}
	h.handled = append(h.handled, delivery)
	return nil
}

func (h *recordHandler) handledCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.handled)
}

func (h *recordHandler) attemptsFor(sequence uint64) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.attempts[sequence]
}

// waitTimeout bounds how long a test waits for an asynchronous condition.
const waitTimeout = 10 * time.Second

// waitFor polls cond until it holds or the wait timeout elapses.
func waitFor(t *testing.T, cond func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.FailNow(t, "timed out", message)
}

func TestPublishReturnsAReceiptAndTheEventReplays(t *testing.T) {
	f := open(t, testConfig(t))

	receipt, err := f.Publish(context.Background(), probe{Fact: "started"})
	require.NoError(t, err)
	require.NotEmpty(t, receipt.ID)
	require.Equal(t, uint64(1), receipt.Sequence,
		"an open fabric has stated nothing, so the first published fact is first in the journal")

	rec := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.RunProjector(ctx, rec) }()

	waitFor(t, func() bool { return rec.count("platform.probe.happened") == 1 },
		"projector never replayed the published event")

	cancel()
	require.NoError(t, <-done, "a canceled projector returns cleanly")
}

// TestOpenStatesNothing pins where readiness belongs. A connected transport is
// not a ready node: the projections have not replayed and the handlers have not
// attached, and only composition knows when they have. An adapter that announced
// itself would be stating something it cannot know.
func TestOpenStatesNothing(t *testing.T) {
	f := open(t, testConfig(t))

	high, err := f.HighWater(context.Background())
	require.NoError(t, err)
	require.Zero(t, high, "opening a fabric adds nothing to the journal")

	require.NoError(t, f.Close(context.Background()))

	// Reopening proves it across the whole lifecycle: neither end wrote anything.
	second := open(t, testConfig(t))
	high, err = second.HighWater(context.Background())
	require.NoError(t, err)
	require.Zero(t, high, "closing a fabric adds nothing either")
}

// TestInfoDescribesTheNodesTransport checks the fabric reports what a node's
// ready event says about it: which adapter and server it is, which journal it is
// bound to, and whether it stores that journal or routes to the nodes that do.
func TestInfoDescribesTheNodesTransport(t *testing.T) {
	cfg := testConfig(t)
	f := open(t, cfg)

	info := f.Info()
	require.Equal(t, Name, info.Adapter)
	require.Equal(t, cfg.ServerName, info.Server)
	require.Equal(t, eventfabric.NewSiteScope(
		testDescriptor.Project, testDescriptor.Environment, testDescriptor.Site).StreamName(), info.Journal)
	require.True(t, info.HostsStorage)
	require.Equal(t, cfg.Replicas, info.Replicas)
}

func TestPublishDeduplicatesByStableIdentity(t *testing.T) {
	f := open(t, testConfig(t))

	first, err := f.Publish(context.Background(), keyedProbe{Key: "unit-1"})
	require.NoError(t, err)
	second, err := f.Publish(context.Background(), keyedProbe{Key: "unit-1"})
	require.NoError(t, err)

	require.Equal(t, first.Sequence, second.Sequence,
		"republishing the same identity collapses onto the first acceptance")
	require.Equal(t, first.ID, second.ID,
		"a duplicate receipt identifies the event that is actually retained")
}

func TestPublishStampsHandlerCausation(t *testing.T) {
	f := open(t, testConfig(t))
	cause, err := f.Publish(context.Background(), probe{Fact: "cause"})
	require.NoError(t, err)

	ctx := eventfabric.CausalContext(context.Background(), eventfabric.Delivery{
		Record: events.Record{Meta: events.Meta{ID: cause.ID}},
	})
	_, err = f.Publish(ctx, keyedProbe{Key: "consequence"})
	require.NoError(t, err)

	rec := &recorder{}
	projectorCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.RunProjector(projectorCtx, rec) }()
	waitFor(t, func() bool { return rec.count("platform.probe.keyed") == 1 }, "projector never replayed consequence")
	delivery, found := rec.first("platform.probe.keyed")
	require.True(t, found)
	require.Equal(t, cause.ID, delivery.Record.CausationID)
	require.Equal(t, cause.ID, delivery.Record.CorrelationID)
	cancel()
	require.NoError(t, <-done)
}

func TestProjectorRebuildsFromDiskAfterRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nats")

	first := open(t, configForDir(t, dir))
	_, err := first.Publish(context.Background(), probe{Fact: "a"})
	require.NoError(t, err)
	_, err = first.Publish(context.Background(), probe{Fact: "b"})
	require.NoError(t, err)
	require.NoError(t, first.Close(context.Background()))

	// A fresh server over the same data directory replays the retained journal.
	second := open(t, configForDir(t, dir))
	rec := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- second.RunProjector(ctx, rec) }()

	waitFor(t, func() bool { return rec.count("platform.probe.happened") == 2 },
		"a restarted projector did not rebuild from the retained journal")
	cancel()
	require.NoError(t, <-done)
}

func TestDurableHandlerReceivesEventsPublishedBeforeItStarted(t *testing.T) {
	f := open(t, testConfig(t))

	// Published before any handler exists: a durable handler still receives them.
	_, err := f.Publish(context.Background(), probe{Fact: "a"})
	require.NoError(t, err)
	_, err = f.Publish(context.Background(), probe{Fact: "b"})
	require.NoError(t, err)

	handler := &recordHandler{name: "probe", routes: []eventfabric.Route{probeRoute(t)}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.RunHandler(ctx, handler) }()

	waitFor(t, func() bool { return handler.handledCount() == 2 },
		"the durable handler did not receive events published before it started")
	cancel()
	require.NoError(t, <-done)
}

func TestHandlerRedeliversUntilItSucceeds(t *testing.T) {
	f := open(t, testConfig(t))

	receipt, err := f.Publish(context.Background(), probe{Fact: "retry me"})
	require.NoError(t, err)

	// Fail the first attempt of each delivery; the second succeeds.
	handler := &recordHandler{
		name:      "probe",
		routes:    []eventfabric.Route{probeRoute(t)},
		failUntil: 1,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.RunHandler(ctx, handler) }()

	waitFor(t, func() bool { return handler.handledCount() == 1 },
		"the handler never succeeded after redelivery")
	require.GreaterOrEqual(t, handler.attemptsFor(receipt.Sequence), 2, "the delivery was retried")
	cancel()
	require.NoError(t, <-done)
}

func TestHandlerExhaustsAfterMaxDeliver(t *testing.T) {
	f := open(t, testConfig(t))

	_, err := f.Publish(context.Background(), probe{Fact: "always fails"})
	require.NoError(t, err)

	handler := &recordHandler{
		name:       "probe",
		routes:     []eventfabric.Route{probeRoute(t)},
		alwaysFail: true,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.RunHandler(ctx, handler) }()

	select {
	case err := <-done:
		require.ErrorIs(t, err, eventfabric.ErrHandlerExhausted,
			"a handler retried to its limit surfaces exhaustion rather than dropping the event")
	case <-time.After(20 * time.Second):
		t.Fatal("handler did not exhaust within the timeout")
	}
}

func TestPublishRejectsAnInvalidEvent(t *testing.T) {
	f := open(t, testConfig(t))

	_, err := f.Publish(context.Background(), badEvent{})
	require.ErrorIs(t, err, eventfabric.ErrInvalidEventType)
}

func TestProjectorFailureStopsCatchUp(t *testing.T) {
	f := open(t, testConfig(t))
	_, err := f.Publish(context.Background(), probe{Fact: "a"})
	require.NoError(t, err)

	// A projector that cannot apply an event stops catch-up rather than skipping.
	rec := &recorder{fail: errors.New("cannot apply")}
	err = f.RunProjector(context.Background(), rec)
	require.ErrorContains(t, err, "cannot apply")
}

func TestReopeningWithAnIncompatibleJournalIsRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping NATS integration test in -short mode")
	}
	dir := filepath.Join(t.TempDir(), "nats")

	first, err := Open(context.Background(), testDescriptor, configForDir(t, dir))
	require.NoError(t, err)
	require.NoError(t, first.Close(context.Background()))

	// Reopening the same journal with a different byte limit is refused.
	incompatible := configForDir(t, dir)
	incompatible.MaxBytes = DefaultMaxBytes / 2
	_, err = Open(context.Background(), testDescriptor, incompatible)
	require.ErrorIs(t, err, eventfabric.ErrIncompatibleJournal)
}

func TestHighWaterAndStateTrackCatchUp(t *testing.T) {
	f := open(t, testConfig(t))

	base, err := f.HighWater(context.Background())
	require.NoError(t, err)
	require.Zero(t, base, "an empty journal has accepted nothing")

	_, err = f.Publish(context.Background(), probe{Fact: "a"})
	require.NoError(t, err)
	high, err := f.HighWater(context.Background())
	require.NoError(t, err)
	require.Equal(t, base+1, high)

	rec := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.RunProjector(ctx, rec) }()

	waitFor(t, func() bool {
		state, err := f.State(context.Background())
		return err == nil && state.CaughtUp && state.Applied >= high
	}, "the projector never caught up to the high-water mark")
	cancel()
	require.NoError(t, <-done)
}

// TestCloseIsIdempotentAndRefusesLaterCalls checks a closed fabric is closed:
// closing again is not a failure, and a call that arrives afterwards is refused
// rather than quietly accepted into a transport that is gone.
func TestCloseIsIdempotentAndRefusesLaterCalls(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping NATS integration test in -short mode")
	}
	dir := filepath.Join(t.TempDir(), "nats")
	f, err := Open(context.Background(), testDescriptor, configForDir(t, dir))
	require.NoError(t, err)

	require.NoError(t, f.Close(context.Background()))
	require.NoError(t, f.Close(context.Background()), "close is idempotent")

	_, err = f.Publish(context.Background(), probe{Fact: "late"})
	require.ErrorIs(t, err, eventfabric.ErrClosed)
	_, err = f.HighWater(context.Background())
	require.ErrorIs(t, err, eventfabric.ErrClosed)
	require.ErrorIs(t, f.RunProjector(context.Background(), &recorder{}), eventfabric.ErrClosed)
}

// TestHandlerPendingReportsRetainedWork is what a node's startup gates on: how
// much of the journal a durable handler still owes an answer for. A handler that
// has not attached is reported as such rather than as having nothing to do,
// because a node may serve only on the second of those.
func TestHandlerPendingReportsRetainedWork(t *testing.T) {
	f := open(t, testConfig(t))
	handler := &recordHandler{
		name:   "pending",
		routes: []eventfabric.Route{probeRoute(t)},
	}

	_, err := f.HandlerPending(context.Background(), handler)
	require.ErrorIs(t, err, eventfabric.ErrHandlerNotAttached,
		"a handler that never ran has not decided it has nothing to do")

	// Retain work for a handler that is not running yet.
	for range 3 {
		_, err := f.Publish(context.Background(), probe{Fact: "queued"})
		require.NoError(t, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.RunHandler(ctx, handler) }()

	waitFor(t, func() bool {
		pending, err := f.HandlerPending(context.Background(), handler)
		return err == nil && pending == 0
	}, "the handler's retained work never drained")
	require.Equal(t, 3, handler.handledCount(), "every retained event reached the handler")

	cancel()
	require.NoError(t, <-done)
}
