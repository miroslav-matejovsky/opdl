package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/operations"
	"github.com/miroslav-matejovsky/opdl/platform/internal/redundancy"
)

// Run starts the platform runtime with the given command-line arguments. It
// loads configuration, validates this process role against the machine
// ownership, and runs either the active runtime or a warm standby until signaled.
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
	role, err := resolveRole(*instance, !descriptor.Instances.Standby.Disabled)
	if err != nil {
		return err
	}
	// One factory per process stamps everything this process states, locally and
	// into the site journal, so origin and occurrence identity are decided once
	// and never by a caller.
	factory, err := events.NewFactory(descriptor, role.String())
	if err != nil {
		return err
	}
	recorder, err := operations.Open("", factory)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, recorder.Close()) }()
	fmt.Println(cfg.Summary(role == redundancy.RoleStandby))
	// The service name lets an operator match this process to an entry in the
	// services list. The platform manages no services; it only reports which one
	// the package says should be running this instance.
	serviceName := "(none stated)"
	if service := descriptor.Instances.Service(role == redundancy.RoleStandby); service != nil {
		serviceName = service.Name
	}
	fmt.Printf("    instance     role=%s standby=%t service=%s\n", role, !descriptor.Instances.Standby.Disabled, serviceName)

	// os.Interrupt is the only signal Windows delivers: the runtime raises it for
	// CTRL_C_EVENT and CTRL_BREAK_EVENT, which is how the service manager and the
	// scenario harness ask for a graceful stop. SIGTERM is never raised here.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx = operations.WithRecorder(ctx, recorder)

	recorder.Record(ctx, ProcessStarted{
		OperationsFile: recorder.Path(),
		StandbyEnabled: !descriptor.Instances.Standby.Disabled,
	})

	runErr = runProcess(ctx, cfg, descriptor, role, factory)
	stopped := ProcessStopped{}
	if runErr != nil {
		stopped.Error = runErr.Error()
	}
	recorder.Record(ctx, stopped)
	return runErr
}

// Serving lives in server.go now, on the listener an instance binds for its whole
// lifetime. There is no helper here that opens a listener and closes a site
// together: an instance's listener outlives its passive composition and is
// handed to its active one, so the two lifetimes are no longer the same and
// cannot be managed by one call.
