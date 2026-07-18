package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/internal/config"
)

// Run starts the platform runtime with the given command-line arguments. It
// loads configuration, decides this process's slot role against the machine
// fence, and runs either the active runtime or a warm standby until signaled.
func Run(args []string) error {
	fs := flag.NewFlagSet("platform", flag.ContinueOnError)
	configPath := fs.String("config", "config.toml", "path to the platform TOML configuration file")
	instance := fs.String("instance", "", "local slot to run as, a or b; required on a machine that runs a warm standby")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	descriptor := cfg.Descriptor()

	// The slot is decided before any socket or storage is opened: a machine that
	// runs a warm standby requires an explicit slot so two service definitions
	// cannot share one local identity, and a machine that opted out rejects a
	// second slot outright.
	slot, err := resolveSlot(*instance, descriptor.Instances.WarmStandby)
	if err != nil {
		return err
	}
	fmt.Println(cfg.Summary())
	fmt.Printf("    instance     slot=%s warm_standby=%t\n", slot, descriptor.Instances.WarmStandby)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return runSlot(ctx, cfg, descriptor, slot)
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
	listen := make(chan error, 1)
	go func() { listen <- srv.ListenAndServe() }()

	select {
	case err := <-listen:
		// The server stopped without being asked to, e.g. its address is taken.
		return errors.Join(listenError(err), site.close(ctx))
	case <-site.stopped:
		fmt.Fprintln(os.Stderr, "platform: the event fabric stopped carrying events; shutting down")
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	err := srv.Shutdown(shutdownCtx)
	if err != nil {
		err = fmt.Errorf("shut down HTTP server: %w", err)
	}
	return errors.Join(err, listenError(<-listen), site.close(ctx))
}

// listenError discards the expected end of a server that was shut down and
// reports anything else with context.
func listenError(err error) error {
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("serve HTTP: %w", err)
}
