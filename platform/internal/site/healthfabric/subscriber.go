package healthfabric

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"sync"

	"github.com/miroslav-matejovsky/opdl/platform/internal/site/healthview"
)

// Subscriber turns what arrives on Subject into observations and applies them.
//
// It is established before local probing starts, and that ordering matters: a
// subscription opened afterwards would miss whatever the site said in between,
// and with no replay to fall back on the gap would only close when every other
// observer's next interval came round.
type Subscriber struct {
	view    *healthview.View
	log     *slog.Logger
	sub     Subscription
	once    sync.Once
	mu      sync.Mutex
	rejects map[RejectReason]uint64
	// seen counts messages delivered, whatever became of them, so a site that
	// has gone silent is distinguishable from one whose messages are all being
	// rejected.
	seen uint64
}

// SubscriberCounters is what a subscriber has to report about itself.
type SubscriberCounters struct {
	// Delivered is how many messages arrived on the subject.
	Delivered uint64
	// Rejected is how many were not well-formed reports about this deployment,
	// by reason.
	Rejected map[RejectReason]uint64
	// Dropped is how many were well-formed but not applied, by the view's reason:
	// an unknown unit, an unexpected observer, or an older report.
	Dropped map[healthview.DropReason]uint64
}

// Subscribe begins delivering site health into view.
//
// It flushes before returning, so the subscription is established at the broker
// rather than merely requested. The caller starts local probing after this
// returns, which is what makes "subscribe before publishing" true rather than
// hopeful.
func Subscribe(ctx context.Context, conn Conn, view *healthview.View, log *slog.Logger) (*Subscriber, error) {
	if conn == nil {
		return nil, fmt.Errorf("health fabric: a connection is required")
	}
	if view == nil {
		return nil, fmt.Errorf("health fabric: a view is required")
	}
	if log == nil {
		log = slog.Default()
	}
	subscriber := &Subscriber{
		view:    view,
		log:     log,
		rejects: make(map[RejectReason]uint64, len(RejectReasons)),
	}
	sub, err := conn.Subscribe(Subject, subscriber.deliver)
	if err != nil {
		return nil, fmt.Errorf("health fabric: subscribe to %s: %w", Subject, err)
	}
	subscriber.sub = sub
	if err := conn.Flush(ctx); err != nil {
		return nil, fmt.Errorf("health fabric: establish the subscription to %s: %w",
			Subject, err)
	}
	return subscriber, nil
}

// deliver handles one message off the wire.
//
// Nothing here can fail loudly. A message that cannot be read is counted and
// dropped, because the alternative — a receiver that stops or panics on a
// malformed payload — would let one bad sender take down every instance's view
// of the site.
func (s *Subscriber) deliver(data []byte) {
	s.mu.Lock()
	s.seen++
	s.mu.Unlock()

	observation, reason, err := decode(s.view.Deployment(), data)
	if reason != "" {
		s.mu.Lock()
		first := s.rejects[reason] == 0
		s.rejects[reason]++
		s.mu.Unlock()
		// The first of a kind is logged and the rest are counted. A sender stuck
		// producing malformed messages produces them at its probe interval, and a
		// log line each would bury everything else.
		if first {
			s.log.Warn("site health message rejected", "reason", string(reason), "error", err.Error())
		}
		return
	}
	if dropped := s.view.Apply(observation); dropped != healthview.DropNone {
		s.log.Debug("site health observation not applied",
			"reason", string(dropped), "machine", observation.Unit.Machine, "service", observation.Unit.Service)
	}
}

// Counters returns what this subscriber has seen.
//
// The view's own tally is read after this subscriber's lock is released. The
// two never reach into each other, so the order is not load-bearing, but a lock
// held across a call into another type is a habit worth not starting.
func (s *Subscriber) Counters() SubscriberCounters {
	s.mu.Lock()
	counters := SubscriberCounters{Delivered: s.seen, Rejected: maps.Clone(s.rejects)}
	s.mu.Unlock()

	counters.Dropped = s.view.Drops()
	return counters
}

// Close stops delivery. It is safe to call more than once.
func (s *Subscriber) Close() {
	s.once.Do(func() {
		if s.sub == nil {
			return
		}
		if err := s.sub.Unsubscribe(); err != nil {
			s.log.Debug("site health subscription could not be closed", "error", err.Error())
		}
	})
}
