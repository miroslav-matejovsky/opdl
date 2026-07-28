package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage"
	"github.com/miroslav-matejovsky/opdl/platform/internal/instance/eventlog"
	"github.com/miroslav-matejovsky/opdl/platform/internal/instance/state"
	"github.com/miroslav-matejovsky/opdl/platform/internal/machine/eventstore"
	"github.com/miroslav-matejovsky/opdl/platform/internal/machine/redundancy"
)

// Run starts the platform runtime with the given command-line arguments. It
// loads configuration, validates this process role against the machine
// ownership, and runs either the active runtime or a warm standby until signaled.
func Run(args []string) (runErr error) {
	// Taken before anything is opened, so the uptime the health endpoints report
	// counts from when the process began rather than from when it became Active.
	started := time.Now()

	fs := flag.NewFlagSet("platform", flag.ContinueOnError)
	instance := fs.String("instance", "", "process role: primary or standby")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Everything this process is configured with is compiled into it, so the only
	// thing a launch decides is which of the machine's two instances this is.
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	descriptor := cfg.Descriptor()

	// Validate the explicit role before opening sockets or storage. Primary-only
	// machines reject standby.
	role, err := resolveRole(*instance, descriptor.HasStandby())
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
	record, err := eventlog.New(instanceOf(descriptor, role).EventsFile)
	if err != nil {
		return err
	}
	// The machine's shared store is opened next, and for the whole process rather
	// than inside the active composition: a machine fact does not wait for a
	// composition to exist before it happens, and ownership moving is stated by
	// the ownership machine, which runs across both states. Only the instance
	// that owns the machine has a machine-scoped fact to state, so the two
	// instances holding it open at once is not two writers.
	machineStore, err := eventstore.Open(descriptor.MachineEventsFile)
	if err != nil {
		return errors.Join(err, record.Close(context.Background()))
	}
	// One publisher, two backends, sorted per envelope: the instance's record
	// takes everything this process states, and the machine's store takes the
	// machine-scoped ones out of that same flow. Which package stated an event
	// does not decide where it lands; its scope does.
	local, err := storage.NewPublisher(factory, record, eventstore.NewBackend(machineStore))
	if err != nil {
		return errors.Join(err, machineStore.Close(context.Background()), record.Close(context.Background()))
	}
	// Closing the process publisher closes the record, and it happens last, after
	// every site has released and every other deferred stop has run.
	defer func() { runErr = errors.Join(runErr, local.Close(context.Background())) }()

	// This process is a new incarnation of the instance, so the epoch advances
	// before it does anything else. It is opened after the record so that a state
	// file that will not read is itself a fact this process can state, and
	// advanced before the process states anything, so no fact this incarnation
	// ever writes carries the previous incarnation's epoch.
	stateStore, err := state.Open(instanceOf(descriptor, role).StateFile)
	if err != nil {
		return err
	}
	incarnation, err := stateStore.Advance(state.ReasonProcessStarted)
	if err != nil {
		return errors.Join(err, local.Publish(context.Background(), EpochAdvanceFailed{
			StateFile: stateStore.Path(),
			Reason:    string(state.ReasonProcessStarted),
			Error:     err.Error(),
		}))
	}

	fmt.Println(cfg.Summary(role == redundancy.RoleStandby))
	// The service name lets an operator match this process to an entry in the
	// services list. The platform manages no services; it only reports which one
	// the package says should be running this instance.
	serviceName := "(none stated)"
	if service := instanceOf(descriptor, role).Service; service != nil {
		serviceName = service.Name
	}
	fmt.Printf("    instance     role=%s standby=%t service=%s\n", role, descriptor.HasStandby(), serviceName)
	// The epoch tells one incarnation of this instance from the previous one,
	// which nothing else in this block can: every other value here is the same
	// after a crash as it was before. The two counts behind it are printed with it
	// because they are what say whether this machine keeps restarting or keeps
	// changing hands.
	fmt.Printf("    epoch        %d (starts %d, activations %d) %s\n",
		incarnation.Epoch, incarnation.ProcessEpoch.Count, incarnation.ActivationEpoch.Count, stateStore.Path())

	// os.Interrupt is the only signal Windows delivers: the runtime raises it for
	// CTRL_C_EVENT and CTRL_BREAK_EVENT, which is how the service manager and the
	// scenario harness ask for a graceful stop. SIGTERM is never raised here.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	proc := process{
		descriptor: descriptor,
		cfg:        cfg,
		role:       role,
		started:    started,
		factory:    factory,
		local:      local,
		record:     record,
		state:      stateStore,
	}

	if err := local.Publish(ctx, ProcessStarted{
		EventsFile:        record.Path(),
		MachineEventsFile: machineStore.Path(),
		StateFile:         stateStore.Path(),
		Epoch:             incarnation.Epoch,
		StandbyEnabled:    descriptor.HasStandby(),
	}); err != nil {
		return err
	}
	// Stated after the opening fact, so the record still begins with the process
	// starting, and stated at all so that every advance of either kind is one
	// platform.app.epoch_advanced: a reader following that type alone sees every
	// incarnation this instance has had, not every one except its launches.
	if err := local.Publish(ctx, epochAdvanced(stateStore.Path(), state.ReasonProcessStarted, incarnation)); err != nil {
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
