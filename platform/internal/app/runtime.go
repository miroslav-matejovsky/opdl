package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/platform/internal/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/eventfabric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/httpapi"
	"github.com/miroslav-matejovsky/opdl/platform/internal/operations"
	"github.com/miroslav-matejovsky/opdl/platform/internal/redundancy"
)

// statusInterval is how often a running process rewrites its local status file. It
// is short enough to be useful to a watching deployment tool and long enough not
// to churn the disk.
const statusInterval = time.Second

const standbyRetryInterval = 200 * time.Millisecond

const unknownLag = "unknown"

type activationKind string

const (
	activationInitial  activationKind = "initial activation"
	activationFailover activationKind = "failover"
	activationFailback activationKind = "failback"
)

// instanceOf returns the running instance's own record: the endpoint it binds and
// the directory it writes.
//
// It reads the descriptor the process was given rather than asking the
// configuration file, because everything a single instance binds or writes is
// resolved onto that instance's record at build time. A machine's two instances
// share one descriptor and one configuration file, so anything read from either
// without a role is a value they would both take.
func instanceOf(descriptor deployment.Descriptor, role redundancy.InstanceRole) deployment.Instance {
	return descriptor.Instances.Get(deployment.Role(role == redundancy.RoleStandby))
}

// resolveRole validates the requested process role against the deployment policy.
//
// Every packaged launch has an explicit role. A machine that opted out rejects
// standby.
func resolveRole(instance string, hasStandby bool) (redundancy.InstanceRole, error) {
	if instance == "" {
		return "", fmt.Errorf("-instance primary|standby is required")
	}
	role, err := redundancy.ParseRole(instance)
	if err != nil {
		return "", err
	}
	if !hasStandby && role == redundancy.RoleStandby {
		return "", fmt.Errorf("this machine does not run a warm standby: only -instance primary is valid")
	}
	return role, nil
}

// runProcess contends for Primary Ownership and runs the active or standby
// composition.
//
// Ownership makes the two instances exclusive. Its holder owns every
// active-only capability; the other process follows the journal. A machine that
// opted out of warm standby rejects the standby role.
func runProcess(ctx context.Context, cfg *config.Config, descriptor deployment.Descriptor, role redundancy.InstanceRole) (runErr error) {
	observer := operations.FromContext(ctx)
	statusPath := redundancy.StatusPath(instanceOf(descriptor, role).RuntimeDir)
	if err := redundancy.PrepareStatusDir(statusPath); err != nil {
		observer.Emit("platform.status_dir_failed", operations.LevelError, "platform.status", "status directory could not be created", map[string]any{operations.AttributeError: err.Error(), operations.AttributePath: statusPath})
		return err
	}

	var windowsMutex string
	if descriptor.Lock != nil {
		windowsMutex = descriptor.Lock.WindowsMutex
	}
	lock, err := redundancy.OpenLock(windowsMutex, role)
	if err != nil {
		observer.Emit("platform.lock_open_failed", operations.LevelError, "platform.redundancy", "ownership object failed to open", map[string]any{operations.AttributeError: err.Error(), operations.AttributeObject: windowsMutex})
		return err
	}
	// The Lock owns kernel handles and a pinned OS thread when not nil. Closing it releases
	// ownership if this process still holds it, so a process that leaves without a
	// clean release still hands over rather than looking like it crashed.
	defer func() { runErr = errors.Join(runErr, lock.Close()) }()

	if lock != nil {
		observer.Emit("platform.lock_opened", operations.LevelInfo, "platform.redundancy", "ownership object opened", map[string]any{
			operations.AttributeObject: lock.Name(),
			// Whether a peer process on this machine already had the object open. Both
			// processes must report the same object; a machine whose two processes
			// report different ones was built from mismatched packages.
			operations.AttributeExisted: lock.Existed(),
		})
	}

	acquired, err := lock.TryAcquire()
	if err != nil {
		return err
	}
	if acquired.Held {
		emitAcquired(observer, lock, acquired)
		return runAsOwner(ctx, cfg, descriptor, role, statusPath, lock, activationInitial)
	}
	observer.Emit("platform.ownership_waiting", operations.LevelInfo, "platform.redundancy", "Primary Ownership is held by the other instance", map[string]any{operations.AttributeObject: lock.Name()})
	if descriptor.Instances.Standby.Disabled {
		return fmt.Errorf("another process already holds Primary Ownership for machine %q and this machine deploys no Standby Instance", descriptor.Machine)
	}
	return runStandby(ctx, cfg, descriptor, role, statusPath, lock)
}

// emitAcquired reports ownership and, crucially, how it was obtained.
//
// An abandoned ownership means the previous owner died rather than handed over. The
// file-lock ownership this replaced could not tell the two apart, so an operator had
// to correlate logs to answer whether a failover was planned.
func emitAcquired(observer *operations.Recorder, lock *redundancy.Lock, acquired redundancy.Acquisition) {
	if lock == nil {
		return
	}
	attributes := map[string]any{
		operations.AttributeObject:    lock.Name(),
		operations.AttributeAbandoned: acquired.Abandoned,
	}
	if acquired.Abandoned {
		observer.Emit("platform.ownership_acquired", operations.LevelWarn, "platform.redundancy",
			"Primary Ownership acquired from a process that died without releasing it", attributes)
		return
	}
	observer.Emit("platform.ownership_acquired", operations.LevelInfo, "platform.redundancy", "Primary Ownership acquired", attributes)
}

// runActive brings this node's Event Fabric up to readiness and serves the public
// API until signaled or until its projection falls too far behind the journal.
func runActive(ctx context.Context, cfg *config.Config, descriptor deployment.Descriptor, role redundancy.InstanceRole, statusPath string) error {
	observer := operations.FromContext(ctx)
	site, err := open(ctx, descriptor, cfg, true, role)
	if err != nil {
		return err
	}

	// This instance's own address, from its own descriptor record. Neither
	// instance can bind the other's, so a failover does not move the endpoint.
	addr := instanceOf(descriptor, role).APIAddress
	var listen net.ListenConfig
	listener, err := listen.Listen(ctx, "tcp", addr)
	if err != nil {
		observer.Emit("platform.api_listen_failed", operations.LevelError, "platform.http", "HTTP API listener failed", map[string]any{"address": addr, operations.AttributeError: err.Error()})
		return errors.Join(fmt.Errorf("listen on %s: %w", addr, err), site.close(ctx))
	}

	// A projection that falls too far behind stops serving rather than answering
	// from a stale view: the status loop cancels serving when it crosses the bound.
	serveCtx, stopServing := context.WithCancel(ctx)
	defer stopServing()
	statusCtx, stopStatus := context.WithCancel(context.WithoutCancel(ctx))
	var lagEvent sync.Once
	onLagExceeded := func() {
		lagEvent.Do(func() {
			observer.Emit("platform.projection_lag_exceeded", operations.LevelError, "platform.status", "projection lag exceeded the configured serving bound", map[string]any{"lag_bound": cfg.LagBound().String()})
		})
		stopServing()
	}
	onStatusFailure := func() {
		observer.Emit("platform.status_write_failed", operations.LevelError, "platform.status", "runtime status file update failed", map[string]any{operations.AttributePath: statusPath})
		stopServing()
	}
	statusDone, err := startStatus(statusCtx, site.fabric, role, redundancy.StateActive, statusPath, cfg.LagBound(), onLagExceeded, onStatusFailure)
	if err != nil {
		stopStatus()
		return errors.Join(err, listener.Close(), site.close(ctx))
	}
	statusStopped := false
	stopActiveStatus := func() error {
		if statusStopped {
			return nil
		}
		statusStopped = true
		stopStatus()
		return <-statusDone
	}

	fmt.Printf("platform: %s active, listening on %s\n", role, addr)
	observer.Emit("platform.api_listening", operations.LevelInfo, "platform.http", "HTTP API is accepting requests", map[string]any{"address": addr})
	srv := &http.Server{
		Addr: addr,
		// exposeSpec is false: the authoritative OpenAPI artifact is
		// api-specifications/openapi.yaml in git, not an endpoint on the runtime.
		Handler:           httpapi.NewHandler(site.commands, site.queries, false),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout(),
	}
	serveErr := serveListener(serveCtx, srv, listener, site, cfg.ShutdownTimeout(), func() error {
		return errors.Join(stopActiveStatus(), writeTransitionStatus(statusPath, role, redundancy.StateStopping))
	})
	stopServing()
	statusErr := stopActiveStatus()
	if statusErr != nil {
		return errors.Join(serveErr, statusErr)
	}
	level := operations.LevelInfo
	attributes := map[string]any{}
	if serveErr != nil {
		level = operations.LevelError
		attributes[operations.AttributeError] = serveErr.Error()
	}
	observer.Emit("platform.api_stopped", level, "platform.http", "HTTP API stopped", attributes)
	return serveErr
}

// runStandby keeps a client-only projection while independently waiting for the
// ownership. Lock acquisition cancels the client composition and starts activation.
func runStandby(ctx context.Context, cfg *config.Config, descriptor deployment.Descriptor, role redundancy.InstanceRole, statusPath string, lock *redundancy.Lock) error {
	waitCtx, stopWaiting := context.WithCancel(ctx)
	defer stopWaiting()
	standbyCtx, stopStandby := context.WithCancel(waitCtx)
	defer stopStandby()

	// The wait happens in the kernel, so this goroutine is parked until
	// the holder releases or dies rather than polling for it.
	ownershipDone := make(chan ownershipResult, 1)
	go func() {
		acquired, err := lock.Acquire(waitCtx)
		if err == nil {
			stopStandby()
		}
		ownershipDone <- ownershipResult{acquired: acquired, err: err}
	}()

	opened, err := openWaitingStandby(ctx, standbyCtx, cfg, descriptor, role, statusPath, lock)
	if err != nil {
		stopWaiting()
		<-ownershipDone
		return err
	}
	standby := opened.site
	acquired, err := awaitOwnership(ctx, waitCtx, cfg, role, statusPath, standby, lock, ownershipDone, stopWaiting)
	if err != nil {
		return err
	}
	emitAcquired(operations.FromContext(ctx), lock, acquired)

	if err := writeTransitionStatus(statusPath, role, redundancy.StateActivating); err != nil {
		var closeErr error
		if standby != nil {
			closeErr = standby.close(ctx)
		}
		return errors.Join(err, closeErr, lock.Release())
	}
	if standby != nil {
		if err := standby.close(ctx); err != nil {
			return errors.Join(err, lock.Release(), writeFailedStatus(statusPath, role, err))
		}
	}

	kind := activationFailover
	if role == redundancy.RolePrimary {
		kind = activationFailback
	}
	return runAsOwner(ctx, cfg, descriptor, role, statusPath, lock, kind)
}

type standbyOpenResult struct {
	site *site
}

func openWaitingStandby(ctx, standbyCtx context.Context, cfg *config.Config, descriptor deployment.Descriptor, role redundancy.InstanceRole, statusPath string, lock *redundancy.Lock) (standbyOpenResult, error) {
	observer := operations.FromContext(ctx)
	attempt := 0
	for !lock.Held() {
		attempt++
		standby, err := open(standbyCtx, descriptor, cfg, false, role)
		if err == nil {
			return standbyOpenResult{site: standby}, nil
		}
		if lock.Held() || ctx.Err() != nil {
			return standbyOpenResult{}, nil
		}
		fmt.Fprintf(os.Stderr, "platform: %s standby projection unavailable: %v; waiting for Primary Ownership\n", role, err)
		if attempt == 1 || attempt%10 == 0 {
			observer.Emit("platform.standby_open_retry", operations.LevelWarn, "platform.redundancy", "standby projection unavailable; waiting for Primary Ownership", map[string]any{"attempt": attempt, operations.AttributeError: err.Error()})
		}
		if writeErr := writeUnavailableStatus(statusPath, role, err); writeErr != nil {
			return standbyOpenResult{}, errors.Join(err, writeErr)
		}
		select {
		case <-time.After(standbyRetryInterval):
		case <-standbyCtx.Done():
		}
	}
	return standbyOpenResult{}, nil
}

// ownershipResult is one ownership acquisition attempt's outcome, carried from the
// waiting goroutine back to the standby that started it.
type ownershipResult struct {
	acquired redundancy.Acquisition
	err      error
}

func awaitOwnership(ctx, waitCtx context.Context, cfg *config.Config, role redundancy.InstanceRole, statusPath string, standby *site, lock *redundancy.Lock, ownershipDone <-chan ownershipResult, stopWaiting context.CancelFunc) (redundancy.Acquisition, error) {
	var statusDone <-chan error
	var stopStatus context.CancelFunc
	if standby != nil && !lock.Held() {
		statusCtx, cancelStatus := context.WithCancel(context.WithoutCancel(waitCtx))
		stopStatus = cancelStatus
		var err error
		statusDone, err = startStatus(statusCtx, standby.fabric, role, redundancy.StatePassive, statusPath, cfg.LagBound(), nil, stopWaiting)
		if err != nil {
			stopStatus()
			stopWaiting()
			<-ownershipDone
			return redundancy.Acquisition{}, errors.Join(err, standby.close(ctx), writeFailedStatus(statusPath, role, err))
		}
		fmt.Printf("platform: %s caught up and waiting for Primary Ownership\n", role)
		operations.FromContext(ctx).Emit("platform.standby_waiting", operations.LevelInfo, "platform.redundancy", "standby projection caught up and is waiting for Primary Ownership", nil)
	}

	result := <-ownershipDone
	if stopStatus != nil {
		stopStatus()
	}
	var statusErr error
	if statusDone != nil {
		statusErr = <-statusDone
	}
	if result.err == nil {
		return result.acquired, statusErr
	}
	var closeErr error
	if standby != nil {
		closeErr = standby.close(ctx)
	}
	if ctx.Err() != nil && statusErr == nil {
		return redundancy.Acquisition{}, errors.Join(writeTransitionStatus(statusPath, role, redundancy.StateStopping), closeErr)
	}
	return redundancy.Acquisition{}, errors.Join(result.err, statusErr, closeErr)
}

func runAsOwner(ctx context.Context, cfg *config.Config, descriptor deployment.Descriptor, role redundancy.InstanceRole, statusPath string, lock *redundancy.Lock, kind activationKind) error {
	observer := operations.FromContext(ctx)
	started := time.Now()
	if err := writeTransitionStatus(statusPath, role, redundancy.StateActivating); err != nil {
		return errors.Join(err, lock.Release())
	}
	fmt.Printf("platform: %s started for %s\n", kind, role)
	observer.Emit("platform.activation_started", operations.LevelInfo, "platform.redundancy", "active runtime activation started", map[string]any{operations.AttributeActivationKind: string(kind)})
	err := runActive(ctx, cfg, descriptor, role, statusPath)
	releaseErr := lock.Release()
	if err != nil {
		observer.Emit("platform.activation_failed", operations.LevelError, "platform.redundancy", "active runtime activation failed", map[string]any{operations.AttributeActivationKind: string(kind), operations.AttributeDurationMS: time.Since(started).Milliseconds(), operations.AttributeError: err.Error()})
		return errors.Join(err, releaseErr, writeFailedStatus(statusPath, role, err))
	}
	fmt.Printf("platform: %s completed for %s\n", kind, role)
	observer.Emit("platform.activation_completed", operations.LevelInfo, "platform.redundancy", "active runtime activation completed", map[string]any{operations.AttributeActivationKind: string(kind), operations.AttributeDurationMS: time.Since(started).Milliseconds()})
	return releaseErr
}

type statusFabric interface {
	State(context.Context) (eventfabric.State, error)
}

// startStatus writes the initial status synchronously, then periodically writes
// live state until ctx ends. A write failure stops the runtime because deployment
// tooling must not act on a stale file during handover or machine shutdown.
func startStatus(ctx context.Context, fabric statusFabric, role redundancy.InstanceRole, state redundancy.State, statusPath string, lagBound time.Duration, onLagExceeded, onFailure func()) (<-chan error, error) {
	var lag redundancy.LagState
	write := func() error {
		now := time.Now()
		status := redundancy.Status{Role: role, State: state, PID: os.Getpid(), UpdatedAt: now.UTC(), FailoverReady: true}
		st, err := fabric.State(ctx)
		behind := lag.Observe(err != nil || !st.CaughtUp, now)
		status.Lag = behind.String()
		if err != nil {
			status.LastError = err.Error()
			status.FailoverReady = false
		} else {
			status.Applied, status.HighWater = st.Applied, st.HighWater
		}
		if redundancy.Exceeds(behind, lagBound) {
			status.FailoverReady = false
			if onLagExceeded != nil {
				onLagExceeded()
			}
		}
		return status.Write(statusPath)
	}

	if err := write(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(statusInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				done <- nil
				return
			case <-ticker.C:
				if err := write(); err != nil {
					if onFailure != nil {
						onFailure()
					}
					done <- err
					return
				}
			}
		}
	}()
	return done, nil
}

// writeFailedStatus records why a process stopped.
func writeFailedStatus(statusPath string, role redundancy.InstanceRole, cause error) error {
	return redundancy.Status{
		Role:      role,
		State:     redundancy.StateFailed,
		PID:       os.Getpid(),
		UpdatedAt: time.Now().UTC(),
		LastError: cause.Error(),
	}.Write(statusPath)
}

func writeTransitionStatus(statusPath string, role redundancy.InstanceRole, state redundancy.State) error {
	return redundancy.Status{
		Role:      role,
		State:     state,
		PID:       os.Getpid(),
		Lag:       unknownLag,
		UpdatedAt: time.Now().UTC(),
	}.Write(statusPath)
}

func writeUnavailableStatus(statusPath string, role redundancy.InstanceRole, cause error) error {
	return redundancy.Status{
		Role:      role,
		State:     redundancy.StatePassive,
		PID:       os.Getpid(),
		Lag:       unknownLag,
		UpdatedAt: time.Now().UTC(),
		LastError: cause.Error(),
	}.Write(statusPath)
}
