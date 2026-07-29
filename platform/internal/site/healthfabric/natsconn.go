package healthfabric

import (
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
// It is named here rather than imported from the package that implements it,
// for the same reason the event fabric names its own: the site level needs one
// capability from the instance level's broker, and naming that capability is
// what keeps this package off the server's.
type InProcessConnProvider interface {
	// InProcessConn returns a connection to the broker that stays inside this
	// process.
	InProcessConn() (net.Conn, error)
}

// NATSConn is a Conn over a real NATS client connection.
//
// It is a separate connection from the event fabric's on purpose. The two carry
// different kinds of traffic under different rules — durable facts against
// expiring current state — and one connection would make a change to either
// have to reason about both. It also means a health subscription that has to be
// torn down does not disturb the connection the health endpoint round-trips on.
type NATSConn struct {
	conn *nats.Conn
}

// Connect opens an in-process client connection to broker, named name.
func Connect(broker InProcessConnProvider, name string) (*NATSConn, error) {
	if broker == nil {
		return nil, fmt.Errorf("health fabric: an embedded broker is required")
	}
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("health fabric: a connection name is required")
	}
	// The empty URL is deliberate: InProcessServer takes the transport from the
	// broker, so there is no address to dial.
	conn, err := nats.Connect("", nats.InProcessServer(broker), nats.Name(name))
	if err != nil {
		return nil, fmt.Errorf("health fabric: connect %q to the embedded broker: %w", name, err)
	}
	return &NATSConn{conn: conn}, nil
}

// Publish sends data on subject. It buffers rather than waiting for the broker,
// which is what lets a probe worker hand over an observation and return.
func (c *NATSConn) Publish(subject string, data []byte) error {
	return c.conn.Publish(subject, data)
}

// Subscribe delivers every message on subject to handler.
func (c *NATSConn) Subscribe(subject string, handler func(data []byte)) (Subscription, error) {
	sub, err := c.conn.Subscribe(subject, func(msg *nats.Msg) { handler(msg.Data) })
	if err != nil {
		return nil, err
	}
	return sub, nil
}

// flushTimeout bounds one flush.
//
// The broker is in this process, so a flush that reaches it takes microseconds
// and anything near this bound is a broker that has stopped reading rather than
// a slow one. It exists because the caller's context is the process's, which
// has no deadline: a flush that inherited it would wait for the process to be
// signalled rather than reporting that the broker is not answering.
const flushTimeout = 5 * time.Second

// Flush waits until everything published so far has reached the broker.
//
// It always applies its own deadline. The NATS client refuses a context without
// one, and the caller's is the process context, so bounding it here is what
// makes establishing a subscription fail with a reason instead of at startup
// with a client error about deadlines.
func (c *NATSConn) Flush(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, flushTimeout)
	defer cancel()
	return c.conn.FlushWithContext(ctx)
}

// Close closes the connection. The caller closes this before the instance's
// broker, so the broker is not shut down under an open connection.
func (c *NATSConn) Close() { c.conn.Close() }
