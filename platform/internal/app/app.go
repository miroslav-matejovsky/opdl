package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/miroslav-matejovsky/opdl/platform/internal/config"
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
	recorder, err := operations.Open(cfg.OperationsEventDir(), descriptor, role.String())
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
	recorder.Emit("platform.process_started", operations.LevelInfo, "platform", "platform process started", map[string]any{
		"operations_file": recorder.Path(),
		"standby_enabled": !descriptor.Instances.Standby.Disabled,
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

// Serving lives in server.go now, on the listener an instance binds for its whole
// lifetime. There is no helper here that opens a listener and closes a site
// together: an instance's listener outlives its passive composition and is
// handed to its active one, so the two lifetimes are no longer the same and
// cannot be managed by one call.
