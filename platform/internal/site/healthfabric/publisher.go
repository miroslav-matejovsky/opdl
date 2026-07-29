package healthfabric

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"

	"github.com/miroslav-matejovsky/opdl/platform/internal/site/healthview"
)

// Conn is the part of a NATS connection this package uses.
//
// It is declared here, beside the code that needs it, rather than taken from
// the package that implements it. What this needs is a subject to publish on, a
// subject to listen to, and a way to know a publication has reached the broker;
// naming that keeps the tests off a real broker and keeps this package from
// depending on a particular client.
type Conn interface {
	// Publish sends data on subject. It is expected to be buffered and to return
	// without waiting for the broker.
	Publish(subject string, data []byte) error
	// Subscribe delivers every message on subject to handler, on whatever
	// goroutine the connection uses.
	Subscribe(subject string, handler func(data []byte)) (Subscription, error)
	// Flush waits until everything published so far has reached the broker, and
	// is what makes a subscription established before the first publication.
	Flush(ctx context.Context) error
}

// Subscription is a delivery this package can stop.
type Subscription interface {
	Unsubscribe() error
}

// Publisher sends this instance's observations to the site.
//
// It never blocks its caller. A probe worker hands over an observation and
// returns to probing, whatever the broker is doing, because a site that cannot
// be reached must not be able to stop a machine watching its own services.
//
// What it holds while it cannot send is the latest observation per service, not
// a queue of them. That bounds the buffer by the machine's service count rather
// than by the length of an outage, and loses nothing: a newer observation is a
// complete statement of the same service's current state, which is all the
// older one was.
type Publisher struct {
	conn     Conn
	identity Identity
	log      *slog.Logger

	// wake carries a single token. A sender that finds it already full has
	// nothing to add: the pending map is where the work is, and one token is
	// enough to say there is some.
	wake chan struct{}
	done chan struct{}
	stop context.CancelFunc
	once sync.Once

	mu sync.Mutex
	// pending is the latest unsent observation per service. Only this machine's
	// own services appear, so it is bounded by what the descriptor authored.
	pending map[string]healthview.Observation
	// sequence orders everything this process publishes. It is assigned when an
	// observation is accepted rather than when it is sent, so the order a
	// receiver fences on is the order the probes happened in.
	sequence uint64
	// sent counts observations that reached the connection. superseded counts
	// those replaced before they were sent, and failed counts those the
	// connection refused. Together they are how an operator tells a quiet site
	// from a machine whose reports are not getting out.
	sent       uint64
	superseded uint64
	failed     uint64
}

// PublisherCounters is what a publisher has to report about itself.
type PublisherCounters struct {
	// Published is how many observations reached the connection.
	Published uint64
	// Superseded is how many were replaced by a newer observation about the same
	// service before they were sent.
	Superseded uint64
	// Failed is how many the connection refused.
	Failed uint64
}

// NewPublisher starts the sender for one instance.
//
// It runs a goroutine that drains the pending map, so the observation a worker
// hands over is sent on some other goroutine than the one that probed. Close
// stops it.
func NewPublisher(conn Conn, identity Identity, log *slog.Logger) (*Publisher, error) {
	if conn == nil {
		return nil, fmt.Errorf("health fabric: a connection is required")
	}
	if err := identity.validate(); err != nil {
		return nil, fmt.Errorf("health fabric: %w", err)
	}
	if log == nil {
		log = slog.Default()
	}
	ctx, stop := context.WithCancel(context.Background())
	publisher := &Publisher{
		conn:     conn,
		identity: identity,
		log:      log,
		wake:     make(chan struct{}, 1),
		done:     make(chan struct{}),
		stop:     stop,
		pending:  make(map[string]healthview.Observation),
	}
	go publisher.run(ctx)
	return publisher, nil
}

// Publish accepts one observation about a service on this machine.
//
// It stamps the observation with this process's identity and its next sequence,
// stores it as that service's latest, and returns. Nothing here touches the
// connection, so a caller is never delayed by the state of the site.
//
// The stamped observation is returned so the caller can apply the same value to
// its own view. Doing it that way rather than letting the caller stamp its own
// copy is what keeps this instance's slot ordered by the numbers its peers
// receive: there is one place a sequence is assigned, and everybody reads the
// same one.
func (p *Publisher) Publish(observation healthview.Observation) healthview.Observation {
	p.mu.Lock()
	p.sequence++
	observation.Unit.Machine = p.identity.Machine
	observation.ObserverRole = p.identity.ObserverRole
	observation.Epoch = p.identity.Epoch
	observation.Sequence = p.sequence
	if _, replaced := p.pending[observation.Unit.Service]; replaced {
		p.superseded++
	}
	p.pending[observation.Unit.Service] = observation
	p.mu.Unlock()

	select {
	case p.wake <- struct{}{}:
	default:
	}
	return observation
}

// Counters returns what this publisher has done so far.
func (p *Publisher) Counters() PublisherCounters {
	p.mu.Lock()
	defer p.mu.Unlock()
	return PublisherCounters{Published: p.sent, Superseded: p.superseded, Failed: p.failed}
}

// Close stops the sender and waits for it to finish.
//
// Whatever is still pending is dropped rather than flushed. A health
// observation from a process that is stopping is about to be superseded by
// nothing at all — the observer is going away, and its silence is what the site
// should see.
func (p *Publisher) Close() {
	p.once.Do(func() {
		p.stop()
		<-p.done
	})
}

// run drains the pending map until its context ends.
func (p *Publisher) run(ctx context.Context) {
	defer close(p.done)
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.wake:
			p.drain()
		}
	}
}

// drain sends everything currently pending.
//
// The map is taken whole and replaced with an empty one, so a worker that
// publishes while this is sending is filling the next batch rather than waiting
// for this one. An observation that fails to send is not put back: by the time
// a retry could happen the next probe has produced a newer one, and re-sending
// the old one would put a receiver's slot behind where it should be.
func (p *Publisher) drain() {
	p.mu.Lock()
	batch := p.pending
	p.pending = make(map[string]healthview.Observation, len(batch))
	p.mu.Unlock()

	for _, service := range slices.Sorted(maps.Keys(batch)) {
		observation := batch[service]
		data, err := encode(p.identity, observation)
		if err != nil {
			p.count(&p.failed)
			p.log.Error("service health observation could not be encoded",
				"service", service, "error", err.Error())
			continue
		}
		if err := p.conn.Publish(Subject, data); err != nil {
			p.count(&p.failed)
			// Logged at debug because a disconnected site produces one of these per
			// service per interval, and the condition an operator needs is the
			// counter rather than the stream.
			p.log.Debug("service health observation could not be published",
				"service", service, "error", err.Error())
			continue
		}
		p.count(&p.sent)
	}
}

func (p *Publisher) count(counter *uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	*counter++
}
