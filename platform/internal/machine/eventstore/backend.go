package eventstore

import (
	"context"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage"
)

var _ storage.Backend = (*Backend)(nil)

// Backend puts a machine store behind a publisher: it passes machine-scoped
// envelopes to the store and lets every other scope by untouched.
//
// It exists because a publisher's backends are not a routing table. One factory
// stamps everything a process states and hands each envelope to every backend,
// and what a process states is a mixture: its own instance-scoped story, the
// machine facts it states while it owns the machine, and the site facts it
// publishes on the site's behalf. A package's level says which level it belongs
// to, not what scope each of its events carries, so the sorting has to happen
// per envelope and this is where it happens.
//
// Passing the rest by is not the same as Append refusing them. Append is a
// caller reaching for the machine's store with something in hand; this is every
// envelope in the process arriving whether or not it was meant for here.
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
