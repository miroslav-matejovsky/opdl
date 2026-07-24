package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// instanceServer is the one listener an instance binds, held for as long as the
// process runs.
//
// It is opened at startup, before the instance knows whether it will be Active,
// and it is never rebound. What changes is the handler behind it: a Passive
// instance answers for itself and refuses domain operations, and an instance that
// takes ownership swaps in the full API.
//
// Binding once matters. The alternative — a Passive instance binding nothing and
// opening its listener on activation — leaves the address free during the
// handover, so a failover has a window in which anything else on the host can
// take the port and the instance then fails to serve at all. It also means a
// Passive instance cannot be asked anything, which is the state an operator most
// often needs to ask about.
//
// Opening the listener at startup has a second effect worth having: an address
// that cannot be bound fails the process immediately, rather than at the moment
// of a failover, which is the worst time to discover it.
type instanceServer struct {
	address  string
	listener net.Listener
	srv      *http.Server
	// handler is swapped on activation and back on step-down. It is read on every
	// request, so the pointer is atomic rather than guarded: serving must not wait
	// on a lock that a transition holds.
	handler atomic.Pointer[http.Handler]
	// inflight counts requests currently being served, whichever handler they
	// started on. It is what drain waits on when an instance steps down without
	// the process stopping.
	inflight atomic.Int64
	// stopped carries the serving goroutine's outcome, so a listener that dies on
	// its own ends the instance instead of leaving it running and unreachable.
	stopped      chan error
	shutdownOnce sync.Once
	shutdownErr  error
}

// openInstanceServer binds address and starts serving initial.
//
// The listener is opened explicitly rather than by http.Server.ListenAndServe so
// that a bind failure is returned here, at startup, instead of arriving
// asynchronously once the process is already running.
func openInstanceServer(ctx context.Context, address string, readHeaderTimeout time.Duration, initial http.Handler) (*instanceServer, error) {
	var listen net.ListenConfig
	listener, err := listen.Listen(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", address, err)
	}
	s := &instanceServer{address: address, listener: listener, stopped: make(chan error, 1)}
	s.handler.Store(&initial)
	s.srv = &http.Server{
		Addr:              address,
		Handler:           s,
		ReadHeaderTimeout: readHeaderTimeout,
	}
	go func() { s.stopped <- s.srv.Serve(listener) }()
	return s, nil
}

// ServeHTTP dispatches to whichever handler this instance is currently serving.
func (s *instanceServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.inflight.Add(1)
	defer s.inflight.Add(-1)
	(*s.handler.Load()).ServeHTTP(w, r)
}

// serveWith replaces the handler without touching the listener, which is how an
// instance changes what it answers without changing where it answers — in both
// directions: activation swaps the full API in, and a step-down swaps the
// Passive surface back while the listener stays bound.
//
// In-flight requests finish against the handler they started on. Toward Active
// that is safe because the Passive handler holds no site resources; toward
// Passive the caller drains before closing what the Active handler could still
// be holding. See drain.
func (s *instanceServer) serveWith(h http.Handler) {
	s.handler.Store(&h)
}

// drain waits until no request is in flight, or until timeout, without closing
// the listener. It is the step-down half of what shutdown does for a process
// stop: after the handler has been swapped back to the Passive surface, draining
// guarantees no request started on the Active handler is still running when the
// site behind it closes.
//
// The count includes requests the Passive handler is serving too. That
// overshoots, but Passive requests are quick health reads, so waiting for a
// moment of quiet converges fast and costs one poll interval of latency at
// worst.
func (s *instanceServer) drain(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for s.inflight.Load() != 0 {
		if time.Now().After(deadline) {
			return fmt.Errorf("drain HTTP requests: %d still in flight after %s", s.inflight.Load(), timeout)
		}
		time.Sleep(5 * time.Millisecond)
	}
	return nil
}

// shutdown stops accepting requests and waits for in-flight ones to drain, or
// until timeout. It is idempotent.
//
// Every caller that closes a site must call this first. A handler still running
// against a closed fabric is the failure the ordering exists to prevent, and it
// is why draining is a wait rather than a listener close.
func (s *instanceServer) shutdown(timeout time.Duration) error {
	s.shutdownOnce.Do(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		err := s.srv.Shutdown(shutdownCtx)
		if err != nil {
			err = fmt.Errorf("shut down HTTP server: %w", err)
		}
		s.shutdownErr = errors.Join(err, listenError(<-s.stopped))
	})
	return s.shutdownErr
}

// listenError discards the expected end of a server that was shut down and
// reports anything else with context.
func listenError(err error) error {
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("serve HTTP: %w", err)
}
