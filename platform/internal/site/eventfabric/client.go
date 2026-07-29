package eventfabric

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

// InProcessConnProvider is an embedded broker a client reaches without a
// socket.
//
// It is declared here, next to the code that uses it, rather than taken from
// the package that implements it. The site level needs one capability from the
// instance level's embedded server — a connection that does not leave the
// process — and naming that capability is what keeps the site's client from
// depending on the instance's server.
type InProcessConnProvider interface {
	// InProcessConn returns a connection to the broker that stays inside this
	// process.
	InProcessConn() (net.Conn, error)
}

// checkTimeout bounds one Check round trip.
//
// The round trip is in-process and takes microseconds, so anything near this
// bound is a fabric that has stopped carrying messages rather than a slow one.
// It exists so a health request always gets an answer: Check is called from the
// health endpoint, and an endpoint that blocked on a wedged broker would report
// nothing at all instead of reporting it as unhealthy.
const checkTimeout = 2 * time.Second

// checkPayload is what one health round trip carries. It is compared on the way
// back, so a delivery that arrived corrupted or from somewhere else fails the
// check rather than passing it.
var checkPayload = []byte("opdl event fabric health check")

// Client is this instance's connection to the site's event fabric.
//
// It is the site level's half of the fabric: the instance level runs the
// embedded broker, and this is what the platform reaches it through. The
// connection is opened once, in the composition root, and held for as long as
// the process runs, whether the instance is Active or Passive.
//
// It carries no publication or consumption yet. Site publication goes through
// events.Publisher and durable consumption is what Consumer describes, and
// Consumer needs the durable replay a JetStream stream provides, which this
// deployment does not run. Until then a Client is the connection and the proof
// that it works.
type Client struct {
	conn *nats.Conn
}

// Connect opens an in-process client connection to broker, named name.
//
// The name is what the broker reports the connection as, so it is required
// rather than defaulted: a connection nobody can attribute is worse than one
// that would not open.
func Connect(broker InProcessConnProvider, name string) (*Client, error) {
	if broker == nil {
		return nil, fmt.Errorf("event fabric: an embedded broker is required")
	}
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("event fabric: a connection name is required")
	}
	// The empty URL is deliberate. InProcessServer takes the transport from the
	// broker itself, so there is no address to dial and no placeholder URL to
	// keep in step with one.
	conn, err := nats.Connect("", nats.InProcessServer(broker), nats.Name(name))
	if err != nil {
		return nil, fmt.Errorf("event fabric: connect %q to the embedded broker: %w", name, err)
	}
	return &Client{conn: conn}, nil
}

// Check reports whether this instance's event fabric is carrying messages, by
// publishing one to a subject of its own and reading it back.
//
// It is a round trip rather than a connection-status read on purpose. A client
// can report itself connected to a broker that has stopped delivering, and the
// question a health endpoint is asking is whether the fabric works, not whether
// a socket-less connection object exists. The subject is new for every call, so
// concurrent checks never read each other's message and none of them leaves a
// subscription behind.
//
// It is bounded by ctx and by checkTimeout, whichever ends first.
func (c *Client) Check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	if !c.conn.IsConnected() {
		return fmt.Errorf("event fabric: the connection to the embedded broker is %s", c.conn.Status())
	}
	subject := nats.NewInbox()
	sub, err := c.conn.SubscribeSync(subject)
	if err != nil {
		return fmt.Errorf("event fabric: subscribe to %s: %w", subject, err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	if err := c.conn.Publish(subject, checkPayload); err != nil {
		return fmt.Errorf("event fabric: publish to %s: %w", subject, err)
	}
	// Publishing is buffered, so the flush is what pushes the message to the
	// broker and reports a broker that stopped reading before the wait below
	// times out with no explanation.
	if err := c.conn.FlushWithContext(ctx); err != nil {
		return fmt.Errorf("event fabric: flush to the embedded broker: %w", err)
	}
	message, err := sub.NextMsgWithContext(ctx)
	if err != nil {
		return fmt.Errorf("event fabric: nothing came back on %s: %w", subject, err)
	}
	if !bytes.Equal(message.Data, checkPayload) {
		return fmt.Errorf("event fabric: %s delivered %d unexpected bytes", subject, len(message.Data))
	}
	return nil
}

// Close closes the connection to the embedded broker.
//
// It returns nothing because closing a client connection reports no outcome.
// The caller closes this before the instance's broker so the broker is not shut
// down under a connection that is still open.
func (c *Client) Close() {
	c.conn.Close()
}
