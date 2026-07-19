package testnet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
)

// Reservation holds a set of reserved loopback TCP addresses and the active
// listeners maintaining their reservation.
type Reservation struct {
	mu        sync.Mutex
	listeners []net.Listener
	addresses []string
}

// Addresses returns a copy of the reserved loopback host-port addresses in
// this reservation.
func (r *Reservation) Addresses() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.addresses...)
}

// Release idempotently closes every listener held by the reservation, allowing
// other processes to bind the reserved addresses. If any listener fails to close,
// their errors are joined and returned, but all listeners are guaranteed to be
// closed or attempted.
func (r *Reservation) Release() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var errs []error
	for _, listener := range r.listeners {
		if err := listener.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	r.listeners = nil
	return errors.Join(errs...)
}

// Reserve reserves count distinct ephemeral IPv4 loopback TCP addresses (127.0.0.1:0)
// and holds their listeners open until the returned Reservation is released.
//
// Holding every listener until the reservation is complete guarantees that all
// returned addresses are distinct. If allocating any listener fails or ctx is
// cancelled, any listeners already allocated during this call are closed before
// returning the error.
func Reserve(ctx context.Context, count int) (*Reservation, error) {
	if count <= 0 {
		return nil, errors.New("count must be positive")
	}

	var lc net.ListenConfig
	listeners := make([]net.Listener, 0, count)
	addresses := make([]string, 0, count)

	for range count {
		if err := ctx.Err(); err != nil {
			for _, l := range listeners {
				_ = l.Close()
			}
			return nil, err
		}

		listener, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
		if err != nil {
			for _, l := range listeners {
				_ = l.Close()
			}
			return nil, fmt.Errorf("reserve loopback address: %w", err)
		}

		listeners = append(listeners, listener)
		addresses = append(addresses, listener.Addr().String())
	}

	return &Reservation{
		listeners: listeners,
		addresses: addresses,
	}, nil
}
