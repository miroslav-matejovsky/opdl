package eventstore

import (
	"context"
	"errors"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

var (
	// ErrScope reports an envelope offered to the machine store that is not
	// machine-scoped. It is a composition error, not an input error: the
	// envelope belongs somewhere else and appending it here would replay one
	// process's story, or a whole site's, as the machine's own.
	//
	// It is returned by Append, which is the deliberate half of this package. A
	// publisher hands every envelope it stamps to every backend it has, so the
	// backend in backend.go passes the machine's on and lets the rest by; only a
	// caller that reached for the appender itself gets this error.
	ErrScope = errors.New("eventstore: envelope is not machine-scoped")

	// ErrClosed reports an operation on a store that has been closed.
	ErrClosed = errors.New("eventstore: store is closed")
)

// Appender is the machine store's whole contract: machine-scoped envelopes go
// in, and nothing comes back out. Implementations must be safe for concurrent
// use by multiple goroutines.
//
// There is no read half. Nothing in the platform reads a machine store back —
// see the package doc for why the reader was dropped rather than deferred — so
// the interface a later SQLite implementation has to satisfy is this one.
type Appender interface {
	// Append stores one machine-scoped envelope. It returns an error wrapping
	// ErrScope for any other scope, and ErrClosed after Close.
	Append(ctx context.Context, envelope events.Envelope) error

	// Close releases the store's write resources. It is idempotent.
	Close(ctx context.Context) error
}
