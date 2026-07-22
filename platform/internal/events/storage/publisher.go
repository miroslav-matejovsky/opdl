package storage

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

var (
	// ErrNoBackends indicates that no backends were provided to NewPublisher.
	ErrNoBackends = errors.New("events/storage: at least one backend is required")

	// ErrClosed indicates that operations were attempted on a closed Publisher.
	ErrClosed = errors.New("events/storage: publisher is closed")
)

// Publisher is a synchronous fan-out publisher that stamps events once using
// events.Factory and writes the resulting envelope to all configured backends.
// It owns backend shutdown via Close.
type Publisher struct {
	factory  events.Factory
	backends []Backend
	mu       sync.Mutex
	closed   bool
}

// NewPublisher creates a new fan-out publisher from the given events.Factory and
// an ordered, non-empty list of backends.
func NewPublisher(factory events.Factory, backends ...Backend) (*Publisher, error) {
	if len(backends) == 0 {
		return nil, ErrNoBackends
	}
	for i, b := range backends {
		if b == nil {
			return nil, fmt.Errorf("events/storage: backend at index %d is nil", i)
		}
	}
	return &Publisher{
		factory:  factory,
		backends: slices.Clone(backends),
	}, nil
}

// Publish stamps event using the configured Factory and passes the resulting
// Envelope synchronously to every backend in configured order.
//
// If a backend returns an error, Publish continues calling the remaining
// backends and returns all encountered errors joined via errors.Join.
// If Publish is called after Close, it returns ErrClosed.
func (p *Publisher) Publish(ctx context.Context, event events.Event) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrClosed
	}
	p.mu.Unlock()

	envelope, err := p.factory.Wrap(ctx, event)
	if err != nil {
		return err
	}

	var errs []error
	for _, b := range p.backends {
		if err := b.Store(ctx, envelope); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// Close shuts down all backends in reverse construction order.
// Subsequent calls to Close are idempotent and return nil.
func (p *Publisher) Close(ctx context.Context) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()

	var errs []error
	for i := len(p.backends) - 1; i >= 0; i-- {
		if err := p.backends[i].Close(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
