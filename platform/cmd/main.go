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
	"github.com/miroslav-matejovsky/opdl/platform/internal/fabric"
	fabricolric "github.com/miroslav-matejovsky/opdl/platform/internal/fabric/olric"
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The recorder is built before anything that records: a configured events
	// directory the platform cannot write is a startup failure, not a surprise
	// on the first event.
	rec, err := newRecorder(descriptor, cfg.EventsDir())
	if err != nil {
		return err
	}

	// The fabric starts before the public API, and a machine that cannot join
	// its site does not serve: answering requests while disconnected from the
	// fabric would be answering for a site this machine is not part of.
	member, err := startFabric(ctx, descriptor, cfg.Fabric().Olric, rec)
	if err != nil {
		return errors.Join(err, closeRecorder(rec))
	}

	registrations, err := registration.NewService(
		registration.Location{Machine: descriptor.Machine, IP: descriptor.IP},
		registration.SingleInstanceCoordinator{},
		rec,
	)
	if err != nil {
		return errors.Join(fmt.Errorf("registration service: %w", err), stopFabric(ctx, member, rec), closeRecorder(rec))
	}

	addr := cfg.Address()
	fmt.Printf("platform: listening on %s\n", addr)
	srv := &http.Server{
		Addr:              addr,
		Handler:           httpapi.NewHandler(registrations),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return serve(ctx, srv, member, rec)
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

// startFabric opens the platform fabric for this machine and records that it is
// ready. Production settings come from the descriptor's topology; the
// configuration file may move the sockets, which is checked before any listener
// opens. A machine that cannot start its fabric returns an error and never
// reaches the public API.
func startFabric(ctx context.Context, descriptor deployment.Descriptor, overrides config.FabricOlric, rec recorder) (fabric.Fabric, error) {
	cfg, err := olricConfig(descriptor.Fabric, overrides)
	if err != nil {
		return nil, err
	}
	f, err := fabricolric.Open(ctx, descriptor.Site, descriptor.Fabric, cfg)
	if err != nil {
		return nil, err
	}

	reachable, err := f.Reachable(ctx)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("fabric members: %w", err), f.Close(ctx))
	}
	fmt.Printf("platform: fabric member %s on %s, %d of %d members reachable\n",
		descriptor.Fabric.Machine, cfg.ClientAddress, len(reachable), len(f.Members()))

	if err := rec.Record(ctx, fabric.Started{
		Adapter: f.Name(),
		Address: cfg.ClientAddress,
		Members: len(reachable),
	}); err != nil {
		return nil, errors.Join(err, f.Close(ctx))
	}
	return f, nil
}

// olricConfig composes the adapter's configuration: the descriptor's derived
// topology first, then whatever the configuration file overrides. Deriving first
// means an absent override is the deployment's own value rather than a blank.
func olricConfig(topology deployment.Fabric, overrides config.FabricOlric) (fabricolric.Config, error) {
	cfg, err := fabricolric.DefaultConfig(topology)
	if err != nil {
		return fabricolric.Config{}, err
	}
	if overrides.ClientAddress != "" {
		cfg.ClientAddress = overrides.ClientAddress
	}
	if overrides.MemberlistAddress != "" {
		cfg.MemberlistAddress = overrides.MemberlistAddress
	}
	// A present but empty list is a deliberate "seed from nobody"; an absent one
	// keeps the peers the descriptor derived.
	if overrides.Join != nil {
		cfg.Join = overrides.Join
	}
	if overrides.StartTimeout != "" {
		timeout, err := time.ParseDuration(overrides.StartTimeout)
		if err != nil {
			return fabricolric.Config{}, fmt.Errorf("fabric: start timeout %q: %w", overrides.StartTimeout, err)
		}
		cfg.StartTimeout = timeout
	}
	return cfg, nil
}

// stopFabric drains and closes the fabric within a bounded context, then records
// that it stopped. The event is recorded while the sink is still open, so an
// orderly shutdown is the last thing an event log shows.
func stopFabric(ctx context.Context, f fabric.Fabric, rec recorder) error {
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()

	var errs []error
	if err := f.Close(stopCtx); err != nil {
		errs = append(errs, fmt.Errorf("close fabric: %w", err))
	}
	if err := rec.Record(stopCtx, fabric.Stopped{Adapter: f.Name()}); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// serve runs srv until it stops on its own or ctx is canceled, then releases the
// runtime's dependencies.
//
// Order matters and is the reverse of startup. HTTP intake stops first and
// in-flight requests drain, so no handler is left calling a closed fabric. Then
// the fabric closes and reports that it stopped, because that fact still has to
// reach the sink. The recorder closes last. Every failure is reported; none
// hides another.
func serve(ctx context.Context, srv *http.Server, f fabric.Fabric, rec recorder) error {
	listen := make(chan error, 1)
	go func() { listen <- srv.ListenAndServe() }()

	select {
	case err := <-listen:
		// The server stopped without being asked to, e.g. its address is taken.
		return errors.Join(listenError(err), stopFabric(ctx, f, rec), closeRecorder(rec))
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err := srv.Shutdown(shutdownCtx)
	if err != nil {
		err = fmt.Errorf("shut down HTTP server: %w", err)
	}
	return errors.Join(err, listenError(<-listen), stopFabric(ctx, f, rec), closeRecorder(rec))
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
