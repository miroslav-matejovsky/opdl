package eventstore

import (
	"context"
	"errors"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

var (
	// ErrScope reports an envelope offered to the machine store that is not
	// machine-scoped.
	ErrScope = errors.New("eventstore: envelope is not machine-scoped")

	// ErrClosed reports an operation on a store that has been closed.
	ErrClosed = errors.New("eventstore: store is closed")
)

// Appender stores machine-scoped envelopes. Implementations must be safe for
// concurrent use.
type Appender interface {
	// Append stores one machine-scoped envelope. It returns an error wrapping
	// ErrScope for any other scope, and ErrClosed after Close.
	Append(ctx context.Context, envelope events.Envelope) error

	// Close releases the store's write resources. It is idempotent.
	Close(ctx context.Context) error
}
