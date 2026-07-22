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
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage/jsonl"
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
	// The local append-only record is opened before anything else this process
	// does, and it is mandatory: every fact the process states has to reach it,
	// including the ones about failing to start. It is opened here rather than
	// with the site because it has to outlive every site the process composes.
	record, err := jsonl.New(instanceOf(descriptor, role).DataDir)
	if err != nil {
		return err
	}
	local, err := storage.NewPublisher(factory, record)
	if err != nil {
		return errors.Join(err, record.Close(context.Background()))
	}
	// Closing the process publisher closes the record, and it happens last, after
	// every site has released and every other deferred stop has run.
	defer func() { runErr = errors.Join(runErr, local.Close(context.Background())) }()

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

	proc := process{
		descriptor: descriptor,
		cfg:        cfg,
		role:       role,
		factory:    factory,
		local:      local,
		record:     record,
	}

	if err := local.Publish(ctx, ProcessStarted{
		EventsFile:     record.Path(),
		StandbyEnabled: !descriptor.Instances.Standby.Disabled,
	}); err != nil {
		return err
	}

	runErr = runProcess(ctx, proc)
	stopped := ProcessStopped{}
	if runErr != nil {
		stopped.Error = runErr.Error()
	}
	// The run's own outcome is the more important of the two, so a failure to
	// state that the process stopped is joined onto it rather than replacing it.
	return errors.Join(runErr, local.Publish(ctx, stopped))
}

// Serving lives in server.go now, on the listener an instance binds for its whole
// lifetime. There is no helper here that opens a listener and closes a site
// together: an instance's listener outlives its passive composition and is
// handed to its active one, so the two lifetimes are no longer the same and
// cannot be managed by one call.
