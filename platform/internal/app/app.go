package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/internal/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/operations"
)

// Run starts the platform runtime with the given command-line arguments. It
// loads configuration, validates this process role against the machine
// fence, and runs either the active runtime or a warm standby until signaled.
func Run(args []string) (runErr error) {
	fs := flag.NewFlagSet("platform", flag.ContinueOnError)
	configPath := fs.String("config", "config.toml", "path to the platform TOML configuration file")
	instance := fs.String("instance", "", "process role: primary or standby")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	descriptor := cfg.Descriptor()

	// Validate the explicit role before opening sockets or storage. Primary-only
	// machines reject standby.
	role, err := resolveRole(*instance, !descriptor.Slots.Standby.Disabled)
	if err != nil {
		return err
	}
	recorder, err := operations.Open(cfg.OperationsEventDir(), descriptor, role.String())
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, recorder.Close()) }()
	fmt.Println(cfg.Summary())
	fmt.Printf("    instance     role=%s standby=%t\n", role, !descriptor.Slots.Standby.Disabled)
	recorder.Emit("platform.process_started", operations.LevelInfo, "platform", "platform process started", map[string]any{
		"operations_file": recorder.Path(),
		"standby_enabled": !descriptor.Slots.Standby.Disabled,
	})

	// os.Interrupt is the only signal Windows delivers: the runtime raises it for
	// CTRL_C_EVENT and CTRL_BREAK_EVENT, which is how the service manager and the
	// scenario harness ask for a graceful stop. SIGTERM is never raised here.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx = operations.WithRecorder(ctx, recorder)

	runErr = runProcess(ctx, cfg, descriptor, role)
	level := operations.LevelInfo
	message := "platform process stopped"
	attributes := map[string]any{}
	if runErr != nil {
		level = operations.LevelError
		message = "platform process failed"
		attributes[operations.AttributeError] = runErr.Error()
	}
	recorder.Emit("platform.process_stopped", level, "platform", message, attributes)
	return runErr
}

// serve runs srv until it stops on its own, its site stops carrying events, or
// ctx is canceled, then releases the runtime's dependencies.
//
// Order matters and is the reverse of startup. HTTP intake stops first and
// in-flight requests drain, so no handler is left calling a closed fabric. Only
// then does the site release: handlers, the node's own stopping event, the
// projector, and the transport, in that order.
//
// A projector or handler that stops on its own ends serving too. The projection
// is what every query is answered from, so a node that stopped folding the
// journal cannot answer for the site any more; serving on would mean quietly
// returning a view the platform already knows is incomplete.
func serve(ctx context.Context, srv *http.Server, site *site, timeout time.Duration) error {
	var listen net.ListenConfig
	listener, err := listen.Listen(ctx, "tcp", srv.Addr)
	if err != nil {
		return errors.Join(fmt.Errorf("serve HTTP: %w", err), site.close(ctx))
	}
	return serveListener(ctx, srv, listener, site, timeout, nil)
}

func serveListener(ctx context.Context, srv *http.Server, listener net.Listener, site *site, timeout time.Duration, onStopping func() error) error {
	listen := make(chan error, 1)
	go func() { listen <- srv.Serve(listener) }()

	select {
	case err := <-listen:
		// The server stopped without being asked to, e.g. its address is taken.
		return errors.Join(listenError(err), site.close(ctx))
	case <-site.stopped:
		fmt.Fprintln(os.Stderr, "platform: the event fabric stopped carrying events; shutting down")
	case <-ctx.Done():
	}
	var transitionErr error
	if onStopping != nil {
		transitionErr = onStopping()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	err := srv.Shutdown(shutdownCtx)
	if err != nil {
		err = fmt.Errorf("shut down HTTP server: %w", err)
	}
	return errors.Join(transitionErr, err, listenError(<-listen), site.close(ctx))
}

// listenError discards the expected end of a server that was shut down and
// reports anything else with context.
func listenError(err error) error {
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("serve HTTP: %w", err)
}
