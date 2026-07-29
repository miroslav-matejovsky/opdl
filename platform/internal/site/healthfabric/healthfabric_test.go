package healthfabric_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/site/healthfabric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/site/healthview"
)

// These run against a fake connection rather than a broker. What this package
// promises is about the wire contract and about never blocking a caller, and
// both are clearer without a server in the way. The real connection is
// exercised end to end by the scenarios.

const settle = 5 * time.Second

var deployment = healthview.Deployment{Project: "customer-a", Environment: "production", Site: "north"}

func discardLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

// fakeConn is a Conn a test drives: it records what was published, delivers
// what the test injects, and can be made to fail.
type fakeConn struct {
	mu        sync.Mutex
	published [][]byte
	publishFn func(data []byte) error
	handler   func(data []byte)
	subErr    error
	flushErr  error
	// sent is signaled after every accepted publication, so a test waits for the
	// sender goroutine rather than polling it.
	sent chan struct{}
}

func newFakeConn() *fakeConn { return &fakeConn{sent: make(chan struct{}, 128)} }

func (c *fakeConn) Publish(_ string, data []byte) error {
	c.mu.Lock()
	fail := c.publishFn
	c.mu.Unlock()
	if fail != nil {
		if err := fail(data); err != nil {
			return err
		}
	}
	c.mu.Lock()
	c.published = append(c.published, data)
	c.mu.Unlock()
	select {
	case c.sent <- struct{}{}:
	default:
	}
	return nil
}

func (c *fakeConn) Subscribe(_ string, handler func(data []byte)) (healthfabric.Subscription, error) {
	if c.subErr != nil {
		return nil, c.subErr
	}
	c.mu.Lock()
	c.handler = handler
	c.mu.Unlock()
	return fakeSubscription{}, nil
}

func (c *fakeConn) Flush(context.Context) error { return c.flushErr }

// deliver hands a message to whatever subscribed, as the broker would.
func (c *fakeConn) deliver(t *testing.T, data []byte) {
	t.Helper()
	c.mu.Lock()
	handler := c.handler
	c.mu.Unlock()
	require.NotNil(t, handler, "nothing has subscribed")
	handler(data)
}

func (c *fakeConn) messages() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([][]byte(nil), c.published...)
}

// awaitSent waits for the next publication to reach the connection.
func (c *fakeConn) awaitSent(t *testing.T) {
	t.Helper()
	select {
	case <-c.sent:
	case <-time.After(settle):
		require.FailNow(t, "the publisher did not send")
	}
}

func (c *fakeConn) failPublishes(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.publishFn = func([]byte) error { return err }
}

type fakeSubscription struct{}

func (fakeSubscription) Unsubscribe() error { return nil }

func testIdentity() healthfabric.Identity {
	return healthfabric.Identity{
		Deployment:   deployment,
		Machine:      "sensor",
		ObserverRole: "primary",
		Epoch:        7,
	}
}

func localObservation(service string, status healthview.Status) healthview.Observation {
	return healthview.Observation{
		Unit:                healthview.UnitKey{Service: service},
		Status:              status,
		CheckedAtUTC:        time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		Latency:             4 * time.Millisecond,
		ConsecutiveFailures: 0,
	}
}

// decodeMessage reads a published payload as the generic JSON it is on the
// wire, so the tests assert the contract rather than a Go type.
func decodeMessage(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var message map[string]any
	require.NoError(t, json.Unmarshal(data, &message))
	return message
}

// TestPublisherStampsIdentityAndOrdersWhatItSends checks a receiver gets what it
// needs to place and fence a report.
//
// The machine, observer role, and epoch come from the publisher rather than
// from the observation, because a process may only ever report about itself.
// The sequence is assigned here so what a receiver fences on is the order the
// probes happened in.
func TestPublisherStampsIdentityAndOrdersWhatItSends(t *testing.T) {
	conn := newFakeConn()
	publisher, err := healthfabric.NewPublisher(conn, testIdentity(), discardLog())
	require.NoError(t, err)
	defer publisher.Close()

	publisher.Publish(localObservation("alarm-service", healthview.StatusHealthy))
	conn.awaitSent(t)

	message := decodeMessage(t, conn.messages()[0])
	require.Equal(t, float64(healthfabric.Version), message["v"])
	require.Equal(t, "customer-a", message["project"])
	require.Equal(t, "production", message["environment"])
	require.Equal(t, "north", message["site"])
	require.Equal(t, "sensor", message["machine"], "a publisher reports only about its own machine")
	require.Equal(t, "alarm-service", message["service"])
	require.Equal(t, "primary", message["observer_role"])
	require.Equal(t, float64(7), message["epoch"], "the process-start epoch, not an ownership epoch")
	require.Equal(t, float64(1), message["sequence"])
	require.Equal(t, "healthy", message["status"])
	require.Equal(t, float64(4), message["latency_ms"])

	publisher.Publish(localObservation("alarm-service", healthview.StatusUnhealthy))
	conn.awaitSent(t)
	require.Equal(t, float64(2), decodeMessage(t, conn.messages()[1])["sequence"],
		"the sequence advances with every observation this process accepts")
}

// TestPublisherOverridesTheMachineAnObservationCarries checks a process cannot
// report about somebody else's machine even if handed an observation that says
// so. Identity is the publisher's, not the payload's.
func TestPublisherOverridesTheMachineAnObservationCarries(t *testing.T) {
	conn := newFakeConn()
	publisher, err := healthfabric.NewPublisher(conn, testIdentity(), discardLog())
	require.NoError(t, err)
	defer publisher.Close()

	observation := localObservation("alarm-service", healthview.StatusHealthy)
	observation.Unit.Machine = "somewhere-else"
	observation.ObserverRole = "standby"
	observation.Epoch = 999
	publisher.Publish(observation)
	conn.awaitSent(t)

	message := decodeMessage(t, conn.messages()[0])
	require.Equal(t, "sensor", message["machine"])
	require.Equal(t, "primary", message["observer_role"])
	require.Equal(t, float64(7), message["epoch"])
}

// TestPublishNeverBlocksAndSupersedesRatherThanQueuing is the property probing
// depends on.
//
// A site that cannot be reached must not be able to stop a machine watching its
// own services. What is held while sending is stalled is the latest observation
// per service, so the buffer is bounded by the machine's service count rather
// than by how long the outage lasted.
func TestPublishNeverBlocksAndSupersedesRatherThanQueuing(t *testing.T) {
	conn := newFakeConn()
	// Hold the sender inside its first publication, so everything after it piles
	// up in the pending map.
	holding := make(chan struct{})
	released := make(chan struct{})
	var once sync.Once
	conn.publishFn = func([]byte) error {
		once.Do(func() {
			close(holding)
			<-released
		})
		return nil
	}
	publisher, err := healthfabric.NewPublisher(conn, testIdentity(), discardLog())
	require.NoError(t, err)
	defer publisher.Close()

	publisher.Publish(localObservation("alarm-service", healthview.StatusHealthy))
	select {
	case <-holding:
	case <-time.After(settle):
		require.FailNow(t, "the publisher never began sending")
	}

	// These all return while the sender is stuck. None of them blocks, and they
	// collapse onto one entry per service.
	for range 100 {
		publisher.Publish(localObservation("alarm-service", healthview.StatusUnhealthy))
		publisher.Publish(localObservation("core-services", healthview.StatusHealthy))
	}
	close(released)

	require.Eventually(t, func() bool {
		counters := publisher.Counters()
		return counters.Published >= 3
	}, settle, 5*time.Millisecond)

	counters := publisher.Counters()
	require.Positive(t, counters.Superseded,
		"observations replaced before they were sent are counted, not silently lost")
	require.Less(t, counters.Published, uint64(201),
		"the buffer holds the latest per service rather than a queue of every attempt")
}

// TestPublisherCountsWhatTheConnectionRefuses checks a disconnected site is
// visible as a counter rather than only as silence.
func TestPublisherCountsWhatTheConnectionRefuses(t *testing.T) {
	conn := newFakeConn()
	conn.failPublishes(errors.New("no responders"))
	publisher, err := healthfabric.NewPublisher(conn, testIdentity(), discardLog())
	require.NoError(t, err)
	defer publisher.Close()

	publisher.Publish(localObservation("alarm-service", healthview.StatusHealthy))
	require.Eventually(t, func() bool {
		return publisher.Counters().Failed > 0
	}, settle, 5*time.Millisecond)
	require.Empty(t, conn.messages())
}

// TestPublisherCloseIsIdempotent checks the composition root can stop it on more
// than one path without the second call panicking on a closed context.
func TestPublisherCloseIsIdempotent(t *testing.T) {
	publisher, err := healthfabric.NewPublisher(newFakeConn(), testIdentity(), discardLog())
	require.NoError(t, err)
	publisher.Close()
	publisher.Close()
}

// TestNewPublisherRejectsAnUnusableIdentity checks a publisher whose messages
// could not be fenced does not start.
func TestNewPublisherRejectsAnUnusableIdentity(t *testing.T) {
	tests := map[string]struct {
		identity healthfabric.Identity
		errText  string
	}{
		"no site": {
			identity: healthfabric.Identity{
				Deployment:   healthview.Deployment{Project: "customer-a", Environment: "production"},
				Machine:      "sensor",
				ObserverRole: "primary", Epoch: 1,
			},
			errText: "site is required",
		},
		"no machine": {
			identity: healthfabric.Identity{Deployment: deployment, ObserverRole: "primary", Epoch: 1},
			errText:  "machine is required",
		},
		"no observer role": {
			identity: healthfabric.Identity{Deployment: deployment, Machine: "sensor", Epoch: 1},
			errText:  "observer role is required",
		},
		"no epoch": {
			identity: healthfabric.Identity{Deployment: deployment, Machine: "sensor", ObserverRole: "primary"},
			errText:  "epoch is required",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			publisher, err := healthfabric.NewPublisher(newFakeConn(), test.identity, discardLog())
			require.ErrorContains(t, err, test.errText)
			require.Nil(t, publisher)
		})
	}
}

// newTestView builds a view of one redundant unit on the sensor machine.
func newTestView(t *testing.T) *healthview.View {
	t.Helper()
	view, err := healthview.New(deployment, []healthview.Unit{{
		UnitKey:        healthview.UnitKey{Machine: "sensor", Service: "alarm-service"},
		MachineProfile: "sensor-node",
		ServiceRole:    "master",
		ObserverRoles:  []string{"primary", "standby"},
		FreshFor:       22 * time.Second,
	}}, healthview.SystemClock{})
	require.NoError(t, err)
	return view
}

// TestSubscriberAppliesWhatItReceives checks a message published by one instance
// reaches another's view intact.
//
// The two halves are exercised together deliberately: what matters is that what
// a publisher writes is what a subscriber can place, and testing either against
// a hand-written payload would let them drift apart.
func TestSubscriberAppliesWhatItReceives(t *testing.T) {
	conn := newFakeConn()
	view := newTestView(t)
	subscriber, err := healthfabric.Subscribe(t.Context(), conn, view, discardLog())
	require.NoError(t, err)
	defer subscriber.Close()

	// A message as the standby instance of the sensor machine would publish it.
	sender := newFakeConn()
	identity := testIdentity()
	identity.ObserverRole = "standby"
	publisher, err := healthfabric.NewPublisher(sender, identity, discardLog())
	require.NoError(t, err)
	defer publisher.Close()
	publisher.Publish(localObservation("alarm-service", healthview.StatusUnhealthy))
	sender.awaitSent(t)

	conn.deliver(t, sender.messages()[0])

	unit := view.Snapshot().Units[0]
	require.Equal(t, healthview.StatusUnhealthy, unit.Status)
	require.Equal(t, []string{"primary"}, unit.MissingObservers)
	require.Len(t, unit.Observations, 1)
	require.Equal(t, "standby", unit.Observations[0].ObserverRole)
	require.Equal(t, uint64(1), subscriber.Counters().Delivered)
}

// TestSubscriberRejectsWhatItCannotRead covers the faults visible in the bytes
// alone, and that each is counted.
//
// None of them may be fatal. A receiver that stopped or panicked on a malformed
// payload would let one bad sender take down every instance's view of the site.
func TestSubscriberRejectsWhatItCannotRead(t *testing.T) {
	valid := map[string]any{
		"v": healthfabric.Version, "project": "customer-a", "environment": "production", "site": "north",
		"machine": "sensor", "service": "alarm-service", "observer_role": "primary",
		"epoch": 1, "sequence": 1, "status": "healthy",
		"checked_at_utc": time.Now().UTC(), "latency_ms": 4, "consecutive_failures": 0,
	}
	mutate := func(changes map[string]any) []byte {
		message := map[string]any{}
		for key, value := range valid {
			message[key] = value
		}
		for key, value := range changes {
			message[key] = value
		}
		data, err := json.Marshal(message)
		require.NoError(t, err)
		return data
	}

	tests := map[string]struct {
		data []byte
		want healthfabric.RejectReason
	}{
		"not json":            {data: []byte("{not json"), want: healthfabric.RejectMalformed},
		"a different version": {data: mutate(map[string]any{"v": 99}), want: healthfabric.RejectVersion},
		"another project":     {data: mutate(map[string]any{"project": "customer-b"}), want: healthfabric.RejectForeign},
		"another environment": {data: mutate(map[string]any{"environment": "staging"}), want: healthfabric.RejectForeign},
		"another site":        {data: mutate(map[string]any{"site": "south"}), want: healthfabric.RejectForeign},
		"no machine":          {data: mutate(map[string]any{"machine": ""}), want: healthfabric.RejectIncomplete},
		"no service":          {data: mutate(map[string]any{"service": ""}), want: healthfabric.RejectIncomplete},
		"no observer role":    {data: mutate(map[string]any{"observer_role": " "}), want: healthfabric.RejectIncomplete},
		"no status":           {data: mutate(map[string]any{"status": ""}), want: healthfabric.RejectIncomplete},
		"no epoch":            {data: mutate(map[string]any{"epoch": 0}), want: healthfabric.RejectImpossible},
		"negative latency":    {data: mutate(map[string]any{"latency_ms": -1}), want: healthfabric.RejectImpossible},
		"absurd latency":      {data: mutate(map[string]any{"latency_ms": 1 << 40}), want: healthfabric.RejectImpossible},
		"negative failures":   {data: mutate(map[string]any{"consecutive_failures": -3}), want: healthfabric.RejectImpossible},
		"oversize": {
			data: mutate(map[string]any{"error": strings.Repeat("x", healthfabric.MaxMessageBytes)}),
			want: healthfabric.RejectOversize,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			conn := newFakeConn()
			view := newTestView(t)
			subscriber, err := healthfabric.Subscribe(t.Context(), conn, view, discardLog())
			require.NoError(t, err)
			defer subscriber.Close()

			conn.deliver(t, test.data)

			counters := subscriber.Counters()
			require.Equal(t, uint64(1), counters.Rejected[test.want])
			require.Equal(t, uint64(1), counters.Delivered, "a rejected message was still delivered")
			require.Equal(t, healthview.StatusUnknown, view.Snapshot().Units[0].Status,
				"nothing about the site changed")
		})
	}
}

// TestSubscriberCountsWhatTheViewWillNotPlace checks a well-formed message about
// something this site does not contain is reported under the view's reason
// rather than as a wire fault.
func TestSubscriberCountsWhatTheViewWillNotPlace(t *testing.T) {
	conn := newFakeConn()
	view := newTestView(t)
	subscriber, err := healthfabric.Subscribe(t.Context(), conn, view, discardLog())
	require.NoError(t, err)
	defer subscriber.Close()

	sender := newFakeConn()
	identity := testIdentity()
	identity.Machine = "unknown-machine"
	publisher, err := healthfabric.NewPublisher(sender, identity, discardLog())
	require.NoError(t, err)
	defer publisher.Close()
	publisher.Publish(localObservation("alarm-service", healthview.StatusHealthy))
	sender.awaitSent(t)

	conn.deliver(t, sender.messages()[0])

	counters := subscriber.Counters()
	require.Empty(t, counters.Rejected, "the message was readable; it was just about somewhere else")
	require.Equal(t, uint64(1), counters.Dropped[healthview.DropUnknownTarget])
}

// TestSubscribeEstablishesBeforeItReturns checks the ordering local probing
// depends on.
//
// A subscription opened after probing starts would miss whatever the site said
// in between, and with no replay the gap would only close when every other
// observer's next interval came round. Flushing is what makes the subscription
// established at the broker rather than merely requested.
func TestSubscribeEstablishesBeforeItReturns(t *testing.T) {
	conn := newFakeConn()
	conn.flushErr = errors.New("broker unreachable")

	subscriber, err := healthfabric.Subscribe(t.Context(), conn, newTestView(t), discardLog())
	require.ErrorContains(t, err, "establish the subscription")
	require.Nil(t, subscriber, "a subscription that was not established is not returned as one that was")
}

// TestSubscribeRejectsAnUnusableComposition checks the guards on the seams.
func TestSubscribeRejectsAnUnusableComposition(t *testing.T) {
	_, err := healthfabric.Subscribe(t.Context(), nil, newTestView(t), discardLog())
	require.ErrorContains(t, err, "a connection is required")

	_, err = healthfabric.Subscribe(t.Context(), newFakeConn(), nil, discardLog())
	require.ErrorContains(t, err, "a view is required")

	conn := newFakeConn()
	conn.subErr = errors.New("subscribe refused")
	_, err = healthfabric.Subscribe(t.Context(), conn, newTestView(t), discardLog())
	require.ErrorContains(t, err, "subscribe to "+healthfabric.Subject)
}

// TestErrorTextIsTruncatedOnTheWire checks one service's failure text cannot
// make every receiver's decode expensive.
//
// The probe error is a service's own text and the only field that varies, so it
// is the one thing a sender bounds before publishing.
func TestErrorTextIsTruncatedOnTheWire(t *testing.T) {
	conn := newFakeConn()
	publisher, err := healthfabric.NewPublisher(conn, testIdentity(), discardLog())
	require.NoError(t, err)
	defer publisher.Close()

	observation := localObservation("alarm-service", healthview.StatusUnhealthy)
	observation.Error = strings.Repeat("x", 4096)
	publisher.Publish(observation)
	conn.awaitSent(t)

	require.Less(t, len(conn.messages()[0]), healthfabric.MaxMessageBytes,
		"a sender keeps its own messages inside the bound receivers enforce")
}
