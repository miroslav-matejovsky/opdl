package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/platform/internal/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/eventfabric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/httpapi"
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
// A machine that runs a warm standby requires an explicit role. A machine that
// opted out defaults to primary and rejects standby.
func resolveRole(instance string, warmStandby bool) (redundancy.ProcessRole, error) {
	if instance == "" {
		if warmStandby {
			return "", fmt.Errorf("this machine runs a warm standby: -instance primary|standby is required")
		}
		return redundancy.RolePrimary, nil
	}
	role, err := redundancy.ParseRole(instance)
	if err != nil {
		return "", err
	}
	if !warmStandby && role == redundancy.RoleStandby {
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
	fencePath := redundancy.FencePath(cfg.InstanceDir(), descriptor.Project, descriptor.Environment, descriptor.Site, descriptor.Machine)
	statusPath := redundancy.StatusPath(cfg.InstanceDir(), descriptor.Project, descriptor.Environment, descriptor.Site, descriptor.Machine, role)

	fence, err := redundancy.OpenFence(fencePath, role)
	if err != nil {
		return err
	}

	acquired, err := fence.TryAcquire()
	if err != nil {
		return err
	}
	if acquired {
		return runFencedActive(ctx, cfg, descriptor, role, statusPath, fence, activationInitial)
	}
	if !descriptor.Instances.WarmStandby {
		return fmt.Errorf("another process already holds the active fence for machine %q and this machine does not run a warm standby", descriptor.Machine)
	}
	return runStandby(ctx, cfg, descriptor, role, statusPath, fence)
}

// runActive brings this node's Event Fabric up to readiness and serves the public
// API until signaled or until its projection falls too far behind the journal.
func runActive(ctx context.Context, cfg *config.Config, descriptor deployment.Descriptor, role redundancy.ProcessRole, statusPath string) error {
	site, err := open(ctx, descriptor, cfg, true, role)
	if err != nil {
		return err
	}

	var listen net.ListenConfig
	listener, err := listen.Listen(ctx, "tcp", cfg.Address())
	if err != nil {
		return errors.Join(fmt.Errorf("listen on %s: %w", cfg.Address(), err), site.close(ctx))
	}

	// A projection that falls too far behind stops serving rather than answering
	// from a stale view: the status loop cancels serving when it crosses the bound.
	serveCtx, stopServing := context.WithCancel(ctx)
	defer stopServing()
	statusCtx, stopStatus := context.WithCancel(context.WithoutCancel(ctx))
	statusDone, err := startStatus(statusCtx, site.fabric, role, redundancy.StateActive, statusPath, cfg.LagBound(), stopServing, stopServing)
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
	srv := &http.Server{
		Addr:              addr,
		Handler:           httpapi.NewHandler(site.commands, site.queries),
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
	for !fence.Held() {
		standby, err := open(standbyCtx, descriptor, cfg, false, role)
		if err == nil {
			return standbyOpenResult{site: standby}, nil
		}
		if fence.Held() || ctx.Err() != nil {
			return standbyOpenResult{}, nil
		}
		fmt.Fprintf(os.Stderr, "platform: %s standby projection unavailable: %v; waiting for the active fence\n", role, err)
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
	if err := writeTransitionStatus(statusPath, role, redundancy.StateActivating); err != nil {
		return errors.Join(err, fence.Release())
	}
	fmt.Printf("platform: %s started for %s\n", kind, role)
	err := runActive(ctx, cfg, descriptor, role, statusPath)
	releaseErr := fence.Release()
	if err != nil {
		return errors.Join(err, releaseErr, writeFailedStatus(statusPath, role, err))
	}
	fmt.Printf("platform: %s completed for %s\n", kind, role)
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
		if err != nil {
			status.LastError = err.Error()
			status.Lag = unknownLag
			status.Promotable = false
		} else {
			status.Applied, status.HighWater = st.Applied, st.HighWater
			behind := lag.Observe(!st.CaughtUp, now)
			status.Lag = behind.String()
			if redundancy.Exceeds(behind, lagBound) {
				status.Promotable = false
				if onLagExceeded != nil {
					onLagExceeded()
				}
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
