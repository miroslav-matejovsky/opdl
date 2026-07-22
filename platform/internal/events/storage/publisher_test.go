package storage_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage"
)

var testDescriptor = config.Descriptor{
	Platform:       "opdl",
	Project:        "scenario",
	Environment:    "development",
	Site:           "local",
	Machine:        "node",
	MachineProfile: "all-in-one",
	IP:             "127.0.0.1",
	Services:       []string{"core-services"},
}

func testFactory() events.Factory {
	factory, err := events.NewFactory(testDescriptor, "primary")
	if err != nil {
		panic(err)
	}
	return factory
}

type testEvent struct {
	Message string `json:"message"`
}

func (testEvent) EventType() events.Type { return "platform.test.happened" }

type taggedIdentifiedEvent struct {
	ID string `json:"id"`
}

func (taggedIdentifiedEvent) EventType() events.Type    { return "platform.test.identified" }
func (t taggedIdentifiedEvent) Tags() []string          { return []string{"warning", "audit"} }
func (t taggedIdentifiedEvent) StableID() string        { return t.ID }
func (taggedIdentifiedEvent) Severity() events.Severity { return events.SeverityWarn }

type invalidEvent struct{}

func (invalidEvent) EventType() events.Type { return "invalid" }

type mockBackend struct {
	mu         sync.Mutex
	name       string
	storeFn    func(ctx context.Context, envelope events.Envelope) error
	closeFn    func(ctx context.Context) error
	envelopes  []events.Envelope
	callLogger func(entry string)
}

func (m *mockBackend) Store(ctx context.Context, env events.Envelope) error {
	m.mu.Lock()
	m.envelopes = append(m.envelopes, env)
	m.mu.Unlock()

	if m.callLogger != nil {
		m.callLogger("store:" + m.name)
	}
	if m.storeFn != nil {
		return m.storeFn(ctx, env)
	}
	return nil
}

func (m *mockBackend) Close(ctx context.Context) error {
	if m.callLogger != nil {
		m.callLogger("close:" + m.name)
	}
	if m.closeFn != nil {
		return m.closeFn(ctx)
	}
	return nil
}

func TestNewPublisherValidation(t *testing.T) {
	factory := testFactory()

	_, err := storage.NewPublisher(factory)
	require.ErrorIs(t, err, storage.ErrNoBackends)

	_, err = storage.NewPublisher(factory, nil)
	require.ErrorContains(t, err, "backend at index 0 is nil")
}

func TestPublishStampsOnceAndDistributesIdenticalEnvelope(t *testing.T) {
	factory := testFactory()
	b1 := &mockBackend{name: "jsonl"}
	b2 := &mockBackend{name: "nats"}

	pub, err := storage.NewPublisher(factory, b1, b2)
	require.NoError(t, err)

	evt := taggedIdentifiedEvent{ID: "item-123"}
	ctx := events.WithCause(t.Context(), events.Envelope{ID: "cause-456", CorrelationID: "corr-789"})

	err = pub.Publish(ctx, evt)
	require.NoError(t, err)

	require.Len(t, b1.envelopes, 1)
	require.Len(t, b2.envelopes, 1)

	env1 := b1.envelopes[0]
	env2 := b2.envelopes[0]

	require.Equal(t, env1, env2, "both backends must receive the exact same envelope")
	require.Equal(t, events.Type("platform.test.identified"), env1.Type)
	require.Equal(t, "cause-456", env1.CausationID)
	require.Equal(t, "corr-789", env1.CorrelationID)
	require.Equal(t, []string{"audit", "warning"}, env1.Tags)
	require.Equal(t, "item-123", env1.StableID)
	require.Equal(t, events.SeverityWarn, env1.Severity)
}

func TestPublishCallsBackendsInConfiguredOrder(t *testing.T) {
	factory := testFactory()
	var callOrder []string
	var mu sync.Mutex
	logCall := func(entry string) {
		mu.Lock()
		defer mu.Unlock()
		callOrder = append(callOrder, entry)
	}

	b1 := &mockBackend{name: "b1", callLogger: logCall}
	b2 := &mockBackend{name: "b2", callLogger: logCall}
	b3 := &mockBackend{name: "b3", callLogger: logCall}

	pub, err := storage.NewPublisher(factory, b1, b2, b3)
	require.NoError(t, err)

	err = pub.Publish(t.Context(), testEvent{Message: "hello"})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"store:b1", "store:b2", "store:b3"}, callOrder)
}

func TestPublishContinuesAfterBackendFailure(t *testing.T) {
	factory := testFactory()

	errJSONL := errors.New("jsonl: disk full")
	errNATS := errors.New("nats: connection refused")

	b1 := &mockBackend{name: "jsonl", storeFn: func(context.Context, events.Envelope) error { return errJSONL }}
	b2 := &mockBackend{name: "nats", storeFn: func(context.Context, events.Envelope) error { return errNATS }}
	b3 := &mockBackend{name: "backup"}

	pub, err := storage.NewPublisher(factory, b1, b2, b3)
	require.NoError(t, err)

	err = pub.Publish(t.Context(), testEvent{Message: "test"})
	require.Error(t, err)

	require.ErrorIs(t, err, errJSONL)
	require.ErrorIs(t, err, errNATS)

	require.Len(t, b1.envelopes, 1)
	require.Len(t, b2.envelopes, 1)
	require.Len(t, b3.envelopes, 1, "b3 must still be attempted despite b1 and b2 failing")
}

func TestPublishRefusesInvalidEvent(t *testing.T) {
	factory := testFactory()
	b1 := &mockBackend{name: "b1"}

	pub, err := storage.NewPublisher(factory, b1)
	require.NoError(t, err)

	err = pub.Publish(t.Context(), invalidEvent{})
	require.Error(t, err)
	require.Len(t, b1.envelopes, 0, "invalid event reaches no backend")
}

func TestPublishPropagatesContextCancellation(t *testing.T) {
	factory := testFactory()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var receivedErr error
	b1 := &mockBackend{
		name: "b1",
		storeFn: func(c context.Context, env events.Envelope) error {
			receivedErr = c.Err()
			return c.Err()
		},
	}

	pub, err := storage.NewPublisher(factory, b1)
	require.NoError(t, err)

	err = pub.Publish(ctx, testEvent{Message: "test"})
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, receivedErr, context.Canceled)
}

func TestCloseInReverseOrderAndIdempotent(t *testing.T) {
	factory := testFactory()

	var callOrder []string
	var mu sync.Mutex
	logCall := func(entry string) {
		mu.Lock()
		defer mu.Unlock()
		callOrder = append(callOrder, entry)
	}

	errB1 := errors.New("jsonl: close err")
	errB3 := errors.New("nats: close err")

	b1 := &mockBackend{name: "b1", callLogger: logCall, closeFn: func(context.Context) error { return errB1 }}
	b2 := &mockBackend{name: "b2", callLogger: logCall}
	b3 := &mockBackend{name: "b3", callLogger: logCall, closeFn: func(context.Context) error { return errB3 }}

	pub, err := storage.NewPublisher(factory, b1, b2, b3)
	require.NoError(t, err)

	err = pub.Close(t.Context())
	require.Error(t, err)
	require.ErrorIs(t, err, errB1)
	require.ErrorIs(t, err, errB3)

	mu.Lock()
	require.Equal(t, []string{"close:b3", "close:b2", "close:b1"}, callOrder)
	mu.Unlock()

	// Idempotent close
	err2 := pub.Close(t.Context())
	require.NoError(t, err2, "second close must be idempotent and return nil")

	mu.Lock()
	require.Len(t, callOrder, 3, "no new close calls made")
	mu.Unlock()
}

func TestPublishAfterClose(t *testing.T) {
	factory := testFactory()
	b1 := &mockBackend{name: "b1"}

	pub, err := storage.NewPublisher(factory, b1)
	require.NoError(t, err)

	err = pub.Close(t.Context())
	require.NoError(t, err)

	err = pub.Publish(t.Context(), testEvent{Message: "after close"})
	require.ErrorIs(t, err, storage.ErrClosed)
	require.Len(t, b1.envelopes, 0)
}

func TestCloseWaitsForActivePublish(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		factory := testFactory()
		storeStarted := make(chan struct{})
		releaseStore := make(chan struct{})
		var storing atomic.Bool
		var closedDuringStore atomic.Bool

		backend := &mockBackend{
			name: "blocking",
			storeFn: func(context.Context, events.Envelope) error {
				storing.Store(true)
				close(storeStarted)
				<-releaseStore
				storing.Store(false)
				return nil
			},
			closeFn: func(context.Context) error {
				closedDuringStore.Store(storing.Load())
				return nil
			},
		}
		pub, err := storage.NewPublisher(factory, backend)
		require.NoError(t, err)

		publishDone := make(chan error, 1)
		go func() { publishDone <- pub.Publish(t.Context(), testEvent{Message: "in flight"}) }()
		<-storeStarted

		closeDone := make(chan error, 1)
		go func() { closeDone <- pub.Close(t.Context()) }()
		synctest.Wait()
		require.False(t, closedDuringStore.Load(),
			"Close must not close a backend while Publish is storing")

		close(releaseStore)
		require.NoError(t, <-publishDone)
		require.NoError(t, <-closeDone)
		require.False(t, closedDuringStore.Load())
	})
}
