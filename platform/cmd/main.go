package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/platform/internal/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/jsonl"
	"github.com/miroslav-matejovsky/opdl/platform/internal/httpapi"
	"github.com/miroslav-matejovsky/opdl/platform/internal/registration"
)

// shutdownTimeout bounds how long in-flight requests have to finish once the
// platform is asked to stop. It is an upper bound, not a delay: an idle server
// shuts down immediately.
const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "platform:", err)
		os.Exit(1)
	}
}

// recorder is the platform's event dependency as the runtime owns it: what the
// registration use case records through, plus the lifecycle the runtime closes.
type recorder interface {
	registration.Recorder
	io.Closer
}

func run(args []string) error {
	fs := flag.NewFlagSet("platform", flag.ContinueOnError)
	configPath := fs.String("config", "config.json", "path to the platform JSON configuration file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	fmt.Println(cfg.Summary())
	descriptor := cfg.Descriptor()

	// The recorder is built before anything that records: a configured events
	// directory the platform cannot write is a startup failure, not a surprise
	// on the first event.
	rec, err := newRecorder(descriptor, cfg.EventsDir())
	if err != nil {
		return err
	}

	registrations, err := registration.NewService(
		registration.Location{Machine: descriptor.Machine, IP: descriptor.IP},
		registration.SingleInstanceCoordinator{},
		rec,
	)
	if err != nil {
		return errors.Join(fmt.Errorf("registration service: %w", err), closeRecorder(rec))
	}

	addr := cfg.Address()
	fmt.Printf("platform: listening on %s\n", addr)
	srv := &http.Server{
		Addr:              addr,
		Handler:           httpapi.NewHandler(registrations),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serve(ctx, srv, rec)
}

// newRecorder builds the platform's event recorder. An empty dir disables
// recording, which is the default: a platform that is not being observed should
// not grow files. Otherwise events are appended to dir, in a file named after
// this machine's own deployment identity.
func newRecorder(descriptor deployment.Descriptor, dir string) (recorder, error) {
	if dir == "" {
		return events.NopRecorder{}, nil
	}
	sink, err := jsonl.Open(dir, events.NodeFromDescriptor(descriptor))
	if err != nil {
		return nil, fmt.Errorf("event sink: %w", err)
	}
	return events.NewRecorder(sink), nil
}

// serve runs srv until it stops on its own or ctx is canceled, then releases the
// runtime's dependencies.
//
// Order matters: HTTP intake stops first and in-flight requests drain, so no
// handler can record into a closed sink, and only then is the recorder closed.
// Both the shutdown error and the close error are reported; neither hides the
// other.
func serve(ctx context.Context, srv *http.Server, rec io.Closer) error {
	listen := make(chan error, 1)
	go func() { listen <- srv.ListenAndServe() }()

	select {
	case err := <-listen:
		// The server stopped without being asked to, e.g. its address is taken.
		return errors.Join(listenError(err), closeRecorder(rec))
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err := srv.Shutdown(shutdownCtx)
	if err != nil {
		err = fmt.Errorf("shut down HTTP server: %w", err)
	}
	return errors.Join(err, listenError(<-listen), closeRecorder(rec))
}

// listenError discards the expected end of a server that was shut down and
// reports anything else with context.
func listenError(err error) error {
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("serve HTTP: %w", err)
}

func closeRecorder(rec io.Closer) error {
	if err := rec.Close(); err != nil {
		return fmt.Errorf("close event recorder: %w", err)
	}
	return nil
}
