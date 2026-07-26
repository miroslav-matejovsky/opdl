package storage

import (
	"context"

	"github.com/miroslav-matejovsky/opdl/platform/internal/instance/events"
)

// Backend receives stamped event envelopes for persistent storage.
// Implementations must be safe for concurrent use by multiple goroutines.
type Backend interface {
	// Store persists envelope to the storage backend.
	Store(ctx context.Context, envelope events.Envelope) error

	// Close shuts down the backend and releases any resources.
	Close(ctx context.Context) error
}

// Delivery is one ordered delivery from an event journal.
type Delivery struct {
	Envelope events.Envelope
	Sequence uint64
}
