package storage

import (
	"context"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// Backend receives stamped event envelopes for persistent storage.
// Implementations must be safe for concurrent use by multiple goroutines.
type Backend interface {
	// Store persists envelope to the storage backend.
	Store(ctx context.Context, envelope events.Envelope) error

	// Close shuts down the backend and releases any resources.
	Close(ctx context.Context) error
}

// Borrowed returns backend with its Close made a no-op, for a Publisher that
// writes to a backend it does not own.
//
// A Publisher closes its backends when it is closed, and a backend that outlives
// the publisher must not be closed with it. The local record is the case this
// exists for: a process opens it before its first Publisher and states facts
// through it after the last one has gone, and a machine that fails over composes
// more than one Publisher over the same record in a single run.
//
// The owner still has to close the real backend. Borrowing hands out the writing
// and keeps the lifetime.
func Borrowed(backend Backend) Backend { return borrowed{backend} }

// borrowed is a Backend that stores through and refuses to close.
type borrowed struct{ Backend }

// Close does nothing. Whoever opened the backend closes it.
func (borrowed) Close(context.Context) error { return nil }
