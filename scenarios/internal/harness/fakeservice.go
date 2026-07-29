package harness

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// This file is the other half of a health scenario: the service the platform is
// watching. A scenario about service health needs a target it can decide the
// answer for, and there is nowhere else to get one — the platform manages no
// services, so nothing it deploys ever listens on a probe port.

// FakeService is one machine's authored service, under a scenario's control.
//
// It binds the address the machine's blueprint named, so the platform reaches it
// through exactly the URL its descriptor composed: the machine's own ip and its
// authored port. Nothing about the probe is simulated. The scenario decides only
// what the service answers.
type FakeService struct {
	// Address is where it listens: the machine's ip and its authored health port.
	Address string
	// Machine is the machine whose service this is, for failure messages.
	Machine string

	server *http.Server
	// healthy is read on every request, so a scenario flips the answer without
	// restarting the listener. Restarting it would produce a connection refusal
	// on the way down and again on the way up, which is a different fault from
	// the one a scenario flipping this is asking about.
	healthy atomic.Bool
	// requests counts what the platform actually asked for, which is how a
	// scenario tells a service nobody probed from one whose answers were ignored.
	requests atomic.Int64
	// serveErr is why the listener stopped, when it stopped for a reason other
	// than being closed. It is stored rather than logged because the goroutine
	// that finds it may outlive the scenario, and t.Log from there panics; the
	// scenario reads it through String when something it asserted has failed.
	serveErr atomic.Pointer[error]
}

// StartService binds a controllable service at the machine's authored health
// address and answers healthy until told otherwise.
//
// It fails the scenario rather than skipping if the address will not bind. The
// port came from the harness's own reservation, so a refusal there is a harness
// fault and not a busy host.
func StartService(t *testing.T, m *Machine) *FakeService {
	t.Helper()
	service := &FakeService{Address: m.Sockets.HealthAddress, Machine: m.Name}
	service.healthy.Store(true)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		service.requests.Add(1)
		if !service.healthy.Load() {
			// A service that is up and knows it is unwell, rather than one that is
			// gone. Both are Unhealthy to the platform, and this is the one a
			// scenario can turn on and off without touching the listener.
			http.Error(w, "unwell", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", service.Address)
	require.NoErrorf(t, err, "%s: the service could not bind %s", m.Name, service.Address)

	service.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := service.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			service.serveErr.Store(&err)
		}
	}()
	t.Cleanup(service.Close)
	return service
}

// Unwell makes the service answer 503 from the next request onwards.
func (s *FakeService) Unwell() { s.healthy.Store(false) }

// Well makes the service answer 200 again.
func (s *FakeService) Well() { s.healthy.Store(true) }

// Requests is how many probes have reached it.
func (s *FakeService) Requests() int64 { return s.requests.Load() }

// Close stops the listener. It is safe to call more than once.
func (s *FakeService) Close() {
	if s.server != nil {
		_ = s.server.Close()
	}
}

// String describes the service for a failure message.
func (s *FakeService) String() string {
	state := "unwell"
	if s.healthy.Load() {
		state = "well"
	}
	described := fmt.Sprintf("service for %s at %s: %s, %d probes received",
		s.Machine, s.Address, state, s.Requests())
	if failure := s.serveErr.Load(); failure != nil {
		described += fmt.Sprintf("; the listener stopped: %v", *failure)
	}
	return described
}
