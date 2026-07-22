package nats

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage/eventfabric"
	"github.com/miroslav-matejovsky/opdl/utils/testnet"
)

var testDescriptor = config.Descriptor{
	Platform: "opdl", Project: "customer-a", Environment: "production",
	Site: "north", Machine: "node", MachineProfile: "all-in-one", IP: "127.0.0.1",
}

func open(t *testing.T, cfg Config) *Backend {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping NATS integration test in -short mode")
	}
	local, _ := localRecord(t)
	b, err := Open(context.Background(), testDescriptor, cfg, local)
	require.NoError(t, err)
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	return b
}

func publisher(t *testing.T, b *Backend) events.Publisher {
	t.Helper()
	factory, err := events.NewFactory(testDescriptor, "primary")
	require.NoError(t, err)
	pub, err := storage.NewPublisher(factory, b)
	require.NoError(t, err)
	return pub
}

func testConfig(t *testing.T) Config {
	t.Helper()
	return configForDir(t, filepath.Join(t.TempDir(), "nats"))
}

func configForDir(t *testing.T, dir string) Config {
	t.Helper()
	res, err := testnet.Reserve(t.Context(), 2)
	require.NoError(t, err)
	require.NoError(t, res.Release())
	addrs := res.Addresses()
	return Config{
		ClientName:        "node-a",
		ServerName:        "node",
		ClusterName:       "test",
		ClientAddress:     addrs[0],
		ClusterAddress:    addrs[1],
		Servers:           []string{addrs[0]},
		HostsStorage:      true,
		JetStreamStoreDir: dir,
		Replicas:          1,
		MaxBytes:          DefaultMaxBytes,
		MaxMessageBytes:   DefaultMaxMessageBytes,
		AckWait:           2 * time.Second,
		MaxDeliver:        3,
		StartupTimeout:    20 * time.Second,
		CatchUpTimeout:    20 * time.Second,
		ShutdownTimeout:   10 * time.Second,
	}
}

func probeRoute(t *testing.T) eventfabric.Route {
	t.Helper()
	route, err := eventfabric.NewRoute(
		eventfabric.NewSiteScope(testDescriptor.Project, testDescriptor.Environment, testDescriptor.Site),
		probe{}.EventType(),
	)
	require.NoError(t, err)
	return route
}

type probe struct {
	Fact string `json:"fact"`
}

func (probe) EventType() events.Type { return "platform.probe.happened" }

type keyedProbe struct {
	Key string `json:"key"`
}

func (keyedProbe) EventType() events.Type { return "platform.probe.keyed" }
func (k keyedProbe) StableID() string     { return k.Key }

type badEvent struct{}

func (badEvent) EventType() events.Type { return "not.routable.extra.tokens" }

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
		if delivery.Envelope.Type == eventType {
			total++
		}
	}
	return total
}

func (r *recorder) first(eventType events.Type) (eventfabric.Delivery, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, delivery := range r.got {
		if delivery.Envelope.Type == eventType {
			return delivery, true
		}
	}
	return eventfabric.Delivery{}, false
}

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

const waitTimeout = 10 * time.Second

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

func TestPublishedEventReplays(t *testing.T) {
	b := open(t, testConfig(t))

	err := publisher(t, b).Publish(context.Background(), probe{Fact: "started"})
	require.NoError(t, err)
	high, err := b.HighWater(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(1), high)

	rec := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- b.RunProjector(ctx, rec) }()

	waitFor(t, func() bool { return rec.count("platform.probe.happened") == 1 },
		"projector never replayed the published event")

	cancel()
	require.NoError(t, <-done)
}

func TestPublishFailsWhenJournalByteLimitIsReached(t *testing.T) {
	cfg := testConfig(t)
	cfg.MaxBytes = 4 << 10
	cfg.MaxMessageBytes = 2 << 10
	b := open(t, cfg)

	err := publisher(t, b).Publish(t.Context(), probe{Fact: strings.Repeat("x", 1024)})
	require.NoError(t, err)

	for range 10 {
		err = publisher(t, b).Publish(t.Context(), probe{Fact: strings.Repeat("x", 1024)})
		if err != nil {
			break
		}
	}
	require.Error(t, err)
	require.ErrorContains(t, err, "publish")
}

func TestOpenStatesNothing(t *testing.T) {
	b := open(t, testConfig(t))

	high, err := b.HighWater(context.Background())
	require.NoError(t, err)
	require.Zero(t, high)

	require.NoError(t, b.Close(context.Background()))

	second := open(t, testConfig(t))
	high, err = second.HighWater(context.Background())
	require.NoError(t, err)
	require.Zero(t, high)
}

func TestInfoDescribesTheNodesTransport(t *testing.T) {
	cfg := testConfig(t)
	b := open(t, cfg)

	info := b.Info()
	require.Equal(t, Name, info.Adapter)
	require.Equal(t, cfg.ServerName, info.Server)
	require.Equal(t, eventfabric.NewSiteScope(
		testDescriptor.Project, testDescriptor.Environment, testDescriptor.Site).StreamName(), info.Journal)
	require.True(t, info.HostsStorage)
	require.Equal(t, cfg.Replicas, info.Replicas)
}

func TestPublishDeduplicatesByStableIdentity(t *testing.T) {
	b := open(t, testConfig(t))

	err := publisher(t, b).Publish(context.Background(), keyedProbe{Key: "unit-1"})
	require.NoError(t, err)
	err = publisher(t, b).Publish(context.Background(), keyedProbe{Key: "unit-1"})
	require.NoError(t, err)

	high, err := b.HighWater(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(1), high)
}

func TestHandlerConsequencesRecordTheirCause(t *testing.T) {
	b := open(t, testConfig(t))
	pub := publisher(t, b)
	err := pub.Publish(context.Background(), probe{Fact: "cause"})
	require.NoError(t, err)

	rec := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	projectorDone := make(chan error, 1)
	go func() { projectorDone <- b.RunProjector(ctx, rec) }()
	waitFor(t, func() bool { return rec.count("platform.probe.happened") == 1 }, "projector never replayed cause")
	causeDelivery, found := rec.first("platform.probe.happened")
	require.True(t, found)

	handler := &consequenceHandler{routes: []eventfabric.Route{probeRoute(t)}, publisher: pub}
	handlerDone := make(chan error, 1)
	go func() { handlerDone <- b.RunHandler(ctx, handler) }()

	waitFor(t, func() bool { return rec.count("platform.probe.keyed") == 1 }, "projector never replayed the consequence")

	delivery, found := rec.first("platform.probe.keyed")
	require.True(t, found)
	require.Equal(t, causeDelivery.Envelope.ID, delivery.Envelope.CausationID)
	require.Equal(t, causeDelivery.Envelope.ID, delivery.Envelope.CorrelationID)

	cancel()
	require.NoError(t, <-handlerDone)
	require.NoError(t, <-projectorDone)
}

type consequenceHandler struct {
	routes    []eventfabric.Route
	publisher events.Publisher
}

func (*consequenceHandler) Name() string                  { return "consequence" }
func (h *consequenceHandler) Routes() []eventfabric.Route { return h.routes }
func (h *consequenceHandler) Handle(ctx context.Context, _ eventfabric.Delivery) error {
	return h.publisher.Publish(ctx, keyedProbe{Key: "consequence"})
}

func TestProjectorRebuildsFromDiskAfterRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nats")

	first := open(t, configForDir(t, dir))
	err := publisher(t, first).Publish(context.Background(), probe{Fact: "a"})
	require.NoError(t, err)
	err = publisher(t, first).Publish(context.Background(), probe{Fact: "b"})
	require.NoError(t, err)
	require.NoError(t, first.Close(context.Background()))

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
	b := open(t, testConfig(t))

	err := publisher(t, b).Publish(context.Background(), probe{Fact: "a"})
	require.NoError(t, err)
	err = publisher(t, b).Publish(context.Background(), probe{Fact: "b"})
	require.NoError(t, err)

	handler := &recordHandler{name: "probe", routes: []eventfabric.Route{probeRoute(t)}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- b.RunHandler(ctx, handler) }()

	waitFor(t, func() bool { return handler.handledCount() == 2 },
		"the durable handler did not receive events published before it started")
	cancel()
	require.NoError(t, <-done)
}

func TestHandlerRedeliversUntilItSucceeds(t *testing.T) {
	b := open(t, testConfig(t))

	err := publisher(t, b).Publish(context.Background(), probe{Fact: "retry me"})
	require.NoError(t, err)
	high, err := b.HighWater(context.Background())
	require.NoError(t, err)

	handler := &recordHandler{
		name:      "probe",
		routes:    []eventfabric.Route{probeRoute(t)},
		failUntil: 1,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- b.RunHandler(ctx, handler) }()

	waitFor(t, func() bool { return handler.handledCount() == 1 },
		"the handler never succeeded after redelivery")
	require.GreaterOrEqual(t, handler.attemptsFor(high), 2)
	cancel()
	require.NoError(t, <-done)
}

func TestHandlerExhaustsAfterMaxDeliver(t *testing.T) {
	b := open(t, testConfig(t))

	err := publisher(t, b).Publish(context.Background(), probe{Fact: "always fails"})
	require.NoError(t, err)

	handler := &recordHandler{
		name:       "probe",
		routes:     []eventfabric.Route{probeRoute(t)},
		alwaysFail: true,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- b.RunHandler(ctx, handler) }()

	select {
	case err := <-done:
		require.ErrorIs(t, err, eventfabric.ErrHandlerExhausted)
	case <-time.After(20 * time.Second):
		t.Fatal("handler did not exhaust within the timeout")
	}
}

func TestPublishRejectsAnInvalidEvent(t *testing.T) {
	b := open(t, testConfig(t))

	err := publisher(t, b).Publish(context.Background(), badEvent{})
	require.ErrorIs(t, err, events.ErrInvalidEventType)

	high, err := b.HighWater(context.Background())
	require.NoError(t, err)
	require.Zero(t, high)
}

func TestStoreStoresTheEnvelopeUnchanged(t *testing.T) {
	b := open(t, testConfig(t))
	factory, err := events.NewFactory(testDescriptor, "primary")
	require.NoError(t, err)
	envelope, err := factory.Wrap(context.Background(), probe{Fact: "unchanged"})
	require.NoError(t, err)

	err = b.Store(context.Background(), envelope)
	require.NoError(t, err)

	rec := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- b.RunProjector(ctx, rec) }()
	waitFor(t, func() bool { return rec.count("platform.probe.happened") == 1 }, "projector never replayed the event")

	delivery, found := rec.first("platform.probe.happened")
	require.True(t, found)
	require.True(t, envelope.OccurredAt.Equal(delivery.Envelope.OccurredAt))
	delivery.Envelope.OccurredAt = envelope.OccurredAt
	require.Equal(t, envelope, delivery.Envelope)

	high, err := b.HighWater(context.Background())
	require.NoError(t, err)
	require.Equal(t, high, delivery.Sequence)

	cancel()
	require.NoError(t, <-done)
}

func TestStoreRefusesAnIncompleteEnvelope(t *testing.T) {
	b := open(t, testConfig(t))
	factory, err := events.NewFactory(testDescriptor, "primary")
	require.NoError(t, err)
	envelope, err := factory.Wrap(context.Background(), probe{Fact: "incomplete"})
	require.NoError(t, err)
	envelope.ID = ""

	err = b.Store(context.Background(), envelope)
	require.ErrorIs(t, err, events.ErrInvalidEnvelope)

	high, err := b.HighWater(context.Background())
	require.NoError(t, err)
	require.Zero(t, high)
}

func TestProjectorFailureStopsCatchUp(t *testing.T) {
	b := open(t, testConfig(t))
	err := publisher(t, b).Publish(context.Background(), probe{Fact: "a"})
	require.NoError(t, err)

	rec := &recorder{fail: errors.New("cannot apply")}
	err = b.RunProjector(context.Background(), rec)
	require.ErrorContains(t, err, "cannot apply")
}

func TestReopeningWithAnIncompatibleJournalIsRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping NATS integration test in -short mode")
	}
	dir := filepath.Join(t.TempDir(), "nats")

	local, _ := localRecord(t)
	first, err := Open(context.Background(), testDescriptor, configForDir(t, dir), local)
	require.NoError(t, err)
	require.NoError(t, first.Close(context.Background()))

	incompatible := configForDir(t, dir)
	incompatible.MaxBytes = DefaultMaxBytes / 2
	_, err = Open(context.Background(), testDescriptor, incompatible, local)
	require.ErrorIs(t, err, eventfabric.ErrIncompatibleJournal)
}

func TestHighWaterAndStateTrackCatchUp(t *testing.T) {
	b := open(t, testConfig(t))

	base, err := b.HighWater(context.Background())
	require.NoError(t, err)
	require.Zero(t, base)

	err = publisher(t, b).Publish(context.Background(), probe{Fact: "a"})
	require.NoError(t, err)
	high, err := b.HighWater(context.Background())
	require.NoError(t, err)
	require.Equal(t, base+1, high)

	rec := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- b.RunProjector(ctx, rec) }()

	waitFor(t, func() bool {
		state, err := b.State(context.Background())
		return err == nil && state.CaughtUp && state.Applied >= high
	}, "the projector never caught up to the high-water mark")
	cancel()
	require.NoError(t, <-done)
}

func TestCloseIsIdempotentAndRefusesLaterCalls(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping NATS integration test in -short mode")
	}
	dir := filepath.Join(t.TempDir(), "nats")
	local, _ := localRecord(t)
	b, err := Open(context.Background(), testDescriptor, configForDir(t, dir), local)
	require.NoError(t, err)

	require.NoError(t, b.Close(context.Background()))
	require.NoError(t, b.Close(context.Background()))

	err = publisher(t, b).Publish(context.Background(), probe{Fact: "late"})
	require.ErrorIs(t, err, eventfabric.ErrClosed)
	_, err = b.HighWater(context.Background())
	require.ErrorIs(t, err, eventfabric.ErrClosed)
	require.ErrorIs(t, b.RunProjector(context.Background(), &recorder{}), eventfabric.ErrClosed)
}

func TestHandlerPendingReportsRetainedWork(t *testing.T) {
	b := open(t, testConfig(t))
	handler := &recordHandler{
		name:   "pending",
		routes: []eventfabric.Route{probeRoute(t)},
	}

	_, err := b.HandlerPending(context.Background(), handler)
	require.ErrorIs(t, err, eventfabric.ErrHandlerNotAttached)

	for range 3 {
		err := publisher(t, b).Publish(context.Background(), probe{Fact: "queued"})
		require.NoError(t, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- b.RunHandler(ctx, handler) }()

	waitFor(t, func() bool {
		pending, err := b.HandlerPending(context.Background(), handler)
		return err == nil && pending == 0
	}, "the handler's retained work never drained")
	require.Equal(t, 3, handler.handledCount())

	cancel()
	require.NoError(t, <-done)
}

func TestUnstartedBackendStoreReturnsErrorWithoutRecursiveEvent(t *testing.T) {
	cfg := loopbackStorageConfig(t)
	local, _ := localRecord(t)
	b, err := New(testDescriptor, cfg, local)
	require.NoError(t, err)

	factory, err := events.NewFactory(testDescriptor, "primary")
	require.NoError(t, err)
	envelope, err := factory.Wrap(context.Background(), probe{Fact: "unstarted"})
	require.NoError(t, err)

	err = b.Store(context.Background(), envelope)
	require.ErrorIs(t, err, eventfabric.ErrClosed)
}
