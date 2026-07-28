package eventstore

import (
	"context"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage"
)

var _ storage.Backend = (*Backend)(nil)

// Backend adapts a machine Appender to storage.Backend. It forwards
// machine-scoped envelopes and ignores every other scope.
type Backend struct {
	appender Appender
}

// NewBackend wraps appender as a publisher backend. It takes ownership of
// appender: closing the Backend closes the store.
func NewBackend(appender Appender) *Backend {
	return &Backend{appender: appender}
}

// Store appends envelope when it is machine-scoped and does nothing otherwise.
func (b *Backend) Store(ctx context.Context, envelope events.Envelope) error {
	if envelope.Scope != events.ScopeMachine {
		return nil
	}
	return b.appender.Append(ctx, envelope)
}

// Close closes the underlying store. It is idempotent.
func (b *Backend) Close(ctx context.Context) error { return b.appender.Close(ctx) }
