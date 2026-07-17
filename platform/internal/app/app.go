package app

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

// recorder is the platform's event dependency as the runtime owns it: what the
// registration use case records through, plus the lifecycle the runtime closes.
type recorder interface {
	registration.Recorder
	io.Closer
}

// Run starts the platform runtime with the given command-line arguments. It
// loads configuration, initializes the event recorder and fabric adapter, starts
// registration reconciliation, and serves the HTTP API until signaled.
func Run(args []string) error {
	fs := flag.NewFlagSet("platform", flag.ContinueOnError)
	configPath := fs.String("config", "config.toml", "path to the platform TOML configuration file")
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

	// Registration opens its collections on the running fabric, so its state is
	// the site's from the first request rather than this process's.
	registrations, reconciler, err := registration.Open(member, rec)
	if err != nil {
		return errors.Join(fmt.Errorf("registration service: %w", err), stopFabric(ctx, member, rec, cfg.ShutdownTimeout()), closeRecorder(rec))
	}

	// The reconciler runs its first pass before the API opens. This machine may
	// have restarted into a site that has been deciding without it, and it owes
	// those requests its answer; serving first would answer questions about a
	// site this instance has not yet looked at.
	interval, err := time.ParseDuration(cfg.Registration().ReconcileInterval)
	if err != nil {
		return errors.Join(fmt.Errorf("registration: reconcile interval %q: %w", cfg.Registration().ReconcileInterval, err), stopFabric(ctx, member, rec, cfg.ShutdownTimeout()), closeRecorder(rec))
	}
	if interval <= 0 {
		return errors.Join(fmt.Errorf("registration: reconcile interval %s is not positive", interval), stopFabric(ctx, member, rec, cfg.ShutdownTimeout()), closeRecorder(rec))
	}
	if err := reconciler.Reconcile(ctx); err != nil {
		return errors.Join(fmt.Errorf("initial registration reconciliation: %w", err), stopFabric(ctx, member, rec, cfg.ShutdownTimeout()), closeRecorder(rec))
	}
	loop := startReconciler(ctx, reconciler, interval)
	fmt.Printf("platform: reconciling registrations every %s\n", interval)

	addr := cfg.Address()
	fmt.Printf("platform: listening on %s\n", addr)
	srv := &http.Server{
		Addr:              addr,
		Handler:           httpapi.NewHandler(registrations),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout(),
	}
	return serve(ctx, srv, loop, member, rec, cfg.ShutdownTimeout())
}

// reconcilerLoop is the running periodic reconciliation, as the runtime holds
// it: something to stop, and something to wait for having stopped.
type reconcilerLoop struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// startReconciler runs periodic reconciliation in the background.
//
// A failed pass is reported and the loop continues: the next pass sees the same
// site and may well succeed, while a platform that stopped reconciling would
// leave every request pending without saying so.
func startReconciler(ctx context.Context, reconciler *registration.Reconciler, interval time.Duration) *reconcilerLoop {
	loopCtx, cancel := context.WithCancel(ctx)
	loop := &reconcilerLoop{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(loop.done)
		_ = reconciler.Run(loopCtx, interval, func(err error) {
			fmt.Fprintln(os.Stderr, "platform: reconcile registrations:", err)
		})
	}()
	return loop
}

// stop ends reconciliation and waits for the pass in flight to finish. Waiting
// is the point: it is what lets the fabric close underneath knowing nothing is
// still reading it.
func (l *reconcilerLoop) stop() {
	l.cancel()
	<-l.done
}

// newRecorder builds the platform's event recorder. An empty dir disables
// recording, which is the default: a platform that is not being observed should
// not grow files. Otherwise events are appended to dir, in a file named after
// this machine's own deployment identity.
func newRecorder(descriptor deployment.Descriptor, dir string) (recorder, error) {
	if dir == "" {
		return events.NopRecorder{}, nil
	}
	node := events.NodeFromDescriptor(descriptor)
	sink, err := jsonl.Open(dir, node)
	if err != nil {
		return nil, fmt.Errorf("event sink: %w", err)
	}
	return events.NewRecorder(node, sink), nil
}

// startFabric opens the platform fabric for this machine and records that it is
// ready. Production settings come from the descriptor's topology; the
// configuration file may move the sockets, which is checked before any listener
// opens. A machine that cannot start its fabric returns an error and never
// reaches the public API.
func startFabric(ctx context.Context, descriptor deployment.Descriptor, overrides config.FabricOlric, rec recorder) (fabric.Fabric, error) {
	cfg, err := olricConfig(descriptor, overrides)
	if err != nil {
		return nil, err
	}
	f, err := fabricolric.Open(ctx, descriptor, cfg)
	if err != nil {
		return nil, err
	}

	reachable, err := f.Reachable(ctx)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("fabric members: %w", err), f.Close(ctx))
	}
	fmt.Printf("platform: fabric member %s on %s, %d of %d members reachable\n",
		descriptor.Machine, cfg.ClientAddress, len(reachable), len(f.Members()))

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
func olricConfig(descriptor deployment.Descriptor, overrides config.FabricOlric) (fabricolric.Config, error) {
	cfg, err := fabricolric.DefaultConfig(descriptor)
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
	if overrides.ShutdownGrace != "" {
		grace, err := time.ParseDuration(overrides.ShutdownGrace)
		if err != nil {
			return fabricolric.Config{}, fmt.Errorf("fabric: shutdown grace %q: %w", overrides.ShutdownGrace, err)
		}
		cfg.ShutdownGrace = grace
	}
	return cfg, nil
}

// stopFabric drains and closes the fabric within a bounded context, then records
// that it stopped. The event is recorded while the sink is still open, so an
// orderly shutdown is the last thing an event log shows.
func stopFabric(ctx context.Context, f fabric.Fabric, rec recorder, timeout time.Duration) error {
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
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
// reconciliation stops and its pass in flight finishes, for the same reason: it
// is the other thing that reads the fabric. Only then does the fabric close and
// report that it stopped, because that fact still has to reach the sink. The
// recorder closes last. Every failure is reported; none hides another.
func serve(ctx context.Context, srv *http.Server, loop *reconcilerLoop, f fabric.Fabric, rec recorder, timeout time.Duration) error {
	listen := make(chan error, 1)
	go func() { listen <- srv.ListenAndServe() }()

	select {
	case err := <-listen:
		// The server stopped without being asked to, e.g. its address is taken.
		loop.stop()
		return errors.Join(listenError(err), stopFabric(ctx, f, rec, timeout), closeRecorder(rec))
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	err := srv.Shutdown(shutdownCtx)
	if err != nil {
		err = fmt.Errorf("shut down HTTP server: %w", err)
	}
	loop.stop()
	return errors.Join(err, listenError(<-listen), stopFabric(ctx, f, rec, timeout), closeRecorder(rec))
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
