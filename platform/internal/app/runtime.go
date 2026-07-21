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
	activationInitial     activationKind = "initial activation"
	activationPromotion   activationKind = "standby promotion"
	activationReclamation activationKind = "primary reclamation"
)

// resolveRole validates the requested process role against the deployment policy.
//
// Every packaged launch has an explicit role. A machine that opted out rejects
// standby.
func resolveRole(instance string, hasStandby bool) (redundancy.ProcessRole, error) {
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

// runProcess contends for the machine fence and runs active or standby composition.
//
// The fence makes the primary and standby exclusive. Its holder owns every
// active-only capability; the other process follows the journal. A machine that
// opted out of warm standby rejects the standby role.
func runProcess(ctx context.Context, cfg *config.Config, descriptor deployment.Descriptor, role redundancy.ProcessRole) error {
	observer := operations.FromContext(ctx)
	fencePath := redundancy.FencePath(cfg.InstanceDir(), descriptor.Project, descriptor.Environment, descriptor.Site, descriptor.Machine)
	statusPath := redundancy.StatusPath(cfg.InstanceDir(), descriptor.Project, descriptor.Environment, descriptor.Site, descriptor.Machine, role)

	fence, err := redundancy.OpenFence(fencePath, role)
	if err != nil {
		observer.Emit("platform.fence_open_failed", operations.LevelError, "platform.redundancy", "machine fence failed to open", map[string]any{operations.AttributeError: err.Error(), operations.AttributePath: fencePath})
		return err
	}

	acquired, err := fence.TryAcquire()
	if err != nil {
		return err
	}
	if acquired {
		observer.Emit("platform.fence_acquired", operations.LevelInfo, "platform.redundancy", "active machine fence acquired", map[string]any{operations.AttributePath: fencePath})
		return runFencedActive(ctx, cfg, descriptor, role, statusPath, fence, activationInitial)
	}
	observer.Emit("platform.fence_waiting", operations.LevelInfo, "platform.redundancy", "active machine fence is held by another process", map[string]any{operations.AttributePath: fencePath})
	if descriptor.Slots.Standby.Disabled {
		return fmt.Errorf("another process already holds the active fence for machine %q and this machine does not run a standby slot", descriptor.Machine)
	}
	return runStandby(ctx, cfg, descriptor, role, statusPath, fence)
}

// runActive brings this node's Event Fabric up to readiness and serves the public
// API until signaled or until its projection falls too far behind the journal.
func runActive(ctx context.Context, cfg *config.Config, descriptor deployment.Descriptor, role redundancy.ProcessRole, statusPath string) error {
	observer := operations.FromContext(ctx)
	site, err := open(ctx, descriptor, cfg, true, role)
	if err != nil {
		return err
	}

	var listen net.ListenConfig
	listener, err := listen.Listen(ctx, "tcp", cfg.Address())
	if err != nil {
		observer.Emit("platform.api_listen_failed", operations.LevelError, "platform.http", "HTTP API listener failed", map[string]any{"address": cfg.Address(), operations.AttributeError: err.Error()})
		return errors.Join(fmt.Errorf("listen on %s: %w", cfg.Address(), err), site.close(ctx))
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

	addr := cfg.Address()
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
// fence. Fence acquisition cancels the client composition and starts activation.
func runStandby(ctx context.Context, cfg *config.Config, descriptor deployment.Descriptor, role redundancy.ProcessRole, statusPath string, fence *redundancy.Fence) error {
	waitCtx, stopWaiting := context.WithCancel(ctx)
	defer stopWaiting()
	standbyCtx, stopStandby := context.WithCancel(waitCtx)
	defer stopStandby()

	fenceDone := make(chan error, 1)
	go func() {
		err := fence.Acquire(waitCtx)
		if err == nil {
			stopStandby()
		}
		fenceDone <- err
	}()

	opened, err := openWaitingStandby(ctx, standbyCtx, cfg, descriptor, role, statusPath, fence)
	if err != nil {
		stopWaiting()
		<-fenceDone
		return err
	}
	standby := opened.site
	if err := awaitFence(ctx, waitCtx, cfg, role, statusPath, standby, fence, fenceDone, stopWaiting); err != nil {
		return err
	}

	if err := writeTransitionStatus(statusPath, role, redundancy.StateActivating); err != nil {
		var closeErr error
		if standby != nil {
			closeErr = standby.close(ctx)
		}
		return errors.Join(err, closeErr, fence.Release())
	}
	if standby != nil {
		if err := standby.close(ctx); err != nil {
			return errors.Join(err, fence.Release(), writeFailedStatus(statusPath, role, err))
		}
	}

	kind := activationPromotion
	if role == redundancy.RolePrimary {
		kind = activationReclamation
	}
	return runFencedActive(ctx, cfg, descriptor, role, statusPath, fence, kind)
}

type standbyOpenResult struct {
	site *site
}

func openWaitingStandby(ctx, standbyCtx context.Context, cfg *config.Config, descriptor deployment.Descriptor, role redundancy.ProcessRole, statusPath string, fence *redundancy.Fence) (standbyOpenResult, error) {
	observer := operations.FromContext(ctx)
	attempt := 0
	for !fence.Held() {
		attempt++
		standby, err := open(standbyCtx, descriptor, cfg, false, role)
		if err == nil {
			return standbyOpenResult{site: standby}, nil
		}
		if fence.Held() || ctx.Err() != nil {
			return standbyOpenResult{}, nil
		}
		fmt.Fprintf(os.Stderr, "platform: %s standby projection unavailable: %v; waiting for the active fence\n", role, err)
		if attempt == 1 || attempt%10 == 0 {
			observer.Emit("platform.standby_open_retry", operations.LevelWarn, "platform.redundancy", "standby projection unavailable; waiting for active fence", map[string]any{"attempt": attempt, operations.AttributeError: err.Error()})
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

func awaitFence(ctx, waitCtx context.Context, cfg *config.Config, role redundancy.ProcessRole, statusPath string, standby *site, fence *redundancy.Fence, fenceDone <-chan error, stopWaiting context.CancelFunc) error {
	var statusDone <-chan error
	var stopStatus context.CancelFunc
	if standby != nil && !fence.Held() {
		statusCtx, cancelStatus := context.WithCancel(context.WithoutCancel(waitCtx))
		stopStatus = cancelStatus
		var err error
		statusDone, err = startStatus(statusCtx, standby.fabric, role, redundancy.StateStandby, statusPath, cfg.LagBound(), nil, stopWaiting)
		if err != nil {
			stopStatus()
			stopWaiting()
			<-fenceDone
			return errors.Join(err, standby.close(ctx), writeFailedStatus(statusPath, role, err))
		}
		fmt.Printf("platform: %s caught up and waiting for the active fence\n", role)
		operations.FromContext(ctx).Emit("platform.standby_waiting", operations.LevelInfo, "platform.redundancy", "standby projection caught up and is waiting for active fence", nil)
	}

	acquireErr := <-fenceDone
	if stopStatus != nil {
		stopStatus()
	}
	var statusErr error
	if statusDone != nil {
		statusErr = <-statusDone
	}
	if acquireErr == nil {
		operations.FromContext(ctx).Emit("platform.fence_acquired", operations.LevelInfo, "platform.redundancy", "standby process acquired active machine fence", nil)
		return statusErr
	}
	var closeErr error
	if standby != nil {
		closeErr = standby.close(ctx)
	}
	if ctx.Err() != nil && statusErr == nil {
		return errors.Join(writeTransitionStatus(statusPath, role, redundancy.StateStopping), closeErr)
	}
	return errors.Join(acquireErr, statusErr, closeErr)
}

func runFencedActive(ctx context.Context, cfg *config.Config, descriptor deployment.Descriptor, role redundancy.ProcessRole, statusPath string, fence *redundancy.Fence, kind activationKind) error {
	observer := operations.FromContext(ctx)
	started := time.Now()
	if err := writeTransitionStatus(statusPath, role, redundancy.StateActivating); err != nil {
		return errors.Join(err, fence.Release())
	}
	fmt.Printf("platform: %s started for %s\n", kind, role)
	observer.Emit("platform.activation_started", operations.LevelInfo, "platform.redundancy", "active runtime activation started", map[string]any{operations.AttributeActivationKind: string(kind)})
	err := runActive(ctx, cfg, descriptor, role, statusPath)
	releaseErr := fence.Release()
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
func startStatus(ctx context.Context, fabric statusFabric, role redundancy.ProcessRole, state redundancy.State, statusPath string, lagBound time.Duration, onLagExceeded, onFailure func()) (<-chan error, error) {
	var lag redundancy.LagState
	write := func() error {
		now := time.Now()
		status := redundancy.Status{Role: role, State: state, PID: os.Getpid(), UpdatedAt: now.UTC(), Promotable: true}
		st, err := fabric.State(ctx)
		behind := lag.Observe(err != nil || !st.CaughtUp, now)
		status.Lag = behind.String()
		if err != nil {
			status.LastError = err.Error()
			status.Promotable = false
		} else {
			status.Applied, status.HighWater = st.Applied, st.HighWater
		}
		if redundancy.Exceeds(behind, lagBound) {
			status.Promotable = false
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
func writeFailedStatus(statusPath string, role redundancy.ProcessRole, cause error) error {
	return redundancy.Status{
		Role:      role,
		State:     redundancy.StateFailed,
		PID:       os.Getpid(),
		UpdatedAt: time.Now().UTC(),
		LastError: cause.Error(),
	}.Write(statusPath)
}

func writeTransitionStatus(statusPath string, role redundancy.ProcessRole, state redundancy.State) error {
	return redundancy.Status{
		Role:      role,
		State:     state,
		PID:       os.Getpid(),
		Lag:       unknownLag,
		UpdatedAt: time.Now().UTC(),
	}.Write(statusPath)
}

func writeUnavailableStatus(statusPath string, role redundancy.ProcessRole, cause error) error {
	return redundancy.Status{
		Role:      role,
		State:     redundancy.StateStandby,
		PID:       os.Getpid(),
		Lag:       unknownLag,
		UpdatedAt: time.Now().UTC(),
		LastError: cause.Error(),
	}.Write(statusPath)
}
