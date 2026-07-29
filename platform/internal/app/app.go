package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/platform/internal/events/storage"
	"github.com/miroslav-matejovsky/opdl/platform/internal/instance/applog"
	"github.com/miroslav-matejovsky/opdl/platform/internal/instance/eventlog"
	"github.com/miroslav-matejovsky/opdl/platform/internal/instance/natsserver"
	"github.com/miroslav-matejovsky/opdl/platform/internal/instance/state"
	"github.com/miroslav-matejovsky/opdl/platform/internal/machine/eventstore"
	"github.com/miroslav-matejovsky/opdl/platform/internal/machine/redundancy"
	"github.com/miroslav-matejovsky/opdl/platform/internal/site/eventfabric"
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
	// The application log is opened first, so every later failure to open
	// something has somewhere to be described. It is closed last, by the deferred
	// close below, after every other deferred stop has run and written what it had
	// to say.
	//
	// Its two base attributes are the process's identity. They are stamped here so
	// no call site repeats them and none can claim to be a different instance,
	// which is the same reason the event factory stamps origin.
	logger, err := applog.Open(instanceOf(descriptor, role).LogFile,
		slog.String("machine", descriptor.Machine),
		slog.String("instance", role.String()),
	)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, logger.Close()) }()
	// Registered after the close, so it runs before it: how this process ended is
	// in the instance's own log file, not only on the stream of a service nobody
	// is watching. It is stated either way, so a log that stops without one is a
	// process that was killed rather than one that left. A failure to close the
	// log is the one thing that cannot be logged, and it is joined onto the
	// outcome above instead.
	defer func() {
		if runErr != nil {
			logger.Error("platform stopped with an error", "error", runErr.Error())
			return
		}
		logger.Info("platform stopped")
	}()
	// Packages below the composition root log through the default logger rather
	// than through one this process hands them. They state facts through the
	// publisher they were given; a diagnostic message is not a fact, and threading
	// a logger through every one of them to carry it would say it was.
	slog.SetDefault(logger.Logger)

	// One factory per process stamps everything this process states, so origin
	// and occurrence identity are decided once and never by a caller.
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
	// The block above is for a person watching a process start. This is the same
	// startup for whoever reads the log file afterwards: the block is not repeated
	// into it, because a wrapped multi-line dump is worse to read as one record
	// than the fields it was rendered from.
	logger.Info("platform starting",
		"service", serviceName,
		"api_address", instanceOf(descriptor, role).APIAddress,
		"standby_enabled", descriptor.HasStandby(),
		"epoch", incarnation.Epoch,
		"events_file", record.Path(),
		"machine_events_file", machineStore.Path(),
		"state_file", stateStore.Path(),
		"log_file", logger.Path(),
	)

	// os.Interrupt is the only signal Windows delivers: the runtime raises it for
	// CTRL_C_EVENT and CTRL_BREAK_EVENT, which is how the service manager and the
	// scenario harness ask for a graceful stop. SIGTERM is never raised here.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

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

	// The instance's embedded broker comes up before the runtime does and stays
	// up for the whole process, in every state it later serves: it is the
	// instance's own infrastructure, like its listener, and not something an
	// activation composes. A broker that will not start stops the process here,
	// where the failure is one thing, rather than at the first activation.
	//
	// The site's client is opened onto it immediately, because a broker nothing
	// is connected to proves nothing. What the runtime is handed is the client's
	// Check, and the health endpoints are what call it.
	fabricConfig := fabricServerConfig(instanceOf(descriptor, role))
	fabricFailed := func(err error) EventFabricStartFailed {
		return EventFabricStartFailed{
			ServerName:     fabricConfig.Name,
			ClusterName:    fabricConfig.ClusterName,
			ClusterAddress: fabricConfig.ClusterAddress,
			Error:          err.Error(),
		}
	}
	broker, err := natsserver.Start(fabricConfig)
	if err != nil {
		return errors.Join(err, local.Publish(ctx, fabricFailed(err)))
	}
	// Registered before the client's close so it runs after it: the broker is not
	// shut down under a connection that is still open.
	defer broker.Close()

	fabric, err := eventfabric.Connect(broker, fabricClientName(descriptor, role))
	if err != nil {
		return errors.Join(err, local.Publish(ctx, fabricFailed(err)))
	}
	defer fabric.Close()

	logger.Info("event fabric started",
		"nats_server_name", fabricConfig.Name,
		"nats_cluster_name", fabricConfig.ClusterName,
		"nats_cluster_address", fabricConfig.ClusterAddress,
		"nats_routes", fabricConfig.Routes,
	)
	if err := local.Publish(ctx, EventFabricStarted{
		ServerName:     fabricConfig.Name,
		ClusterName:    fabricConfig.ClusterName,
		ClusterAddress: fabricConfig.ClusterAddress,
		Routes:         fabricConfig.Routes,
	}); err != nil {
		return err
	}

	proc := process{
		descriptor:   descriptor,
		cfg:          cfg,
		role:         role,
		started:      started,
		local:        local,
		state:        stateStore,
		fabricHealth: fabric.Check,
		log:          logger.Logger,
	}

	// Service monitoring is composed here, at process lifetime, rather than
	// inside an activation. Both of a machine's instances probe every service on
	// it in every ownership state: a service does not stop needing to be watched
	// because the process watching it stepped down, and ownership can move many
	// times over one process's life without a target being affected either way.
	//
	// It starts after the broker, because it opens its own connection to it, and
	// before ownership management, so a Passive instance is probing and hearing
	// from the site from its first moment rather than from its first activation.
	//
	// The epoch it publishes under is the durable instance epoch captured after
	// this process advanced it, before the runtime opened anything. It is what
	// makes a restarted observer's first report supersede everything its
	// previous incarnation said. Later activation advances do not change this
	// publisher's captured value.
	health, err := startServiceHealth(ctx, proc, broker, incarnation.Epoch)
	if err != nil {
		return errors.Join(err, local.Publish(ctx, ServiceHealthStartFailed{Error: err.Error()}))
	}
	// Registered after the fabric's close so it runs before it: probing and the
	// health connection stop before the broker they run on is shut down.
	defer health.Stop()
	// The runtime is handed the rendering of the view rather than the subsystem
	// holding it, so nothing below here can stop, restart, or write to monitoring
	// while answering a query about it. This is the last thing composed onto the
	// process value, because it is the only one that cannot exist until the thing
	// it reads does.
	proc.serviceHealth = health.Response

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
