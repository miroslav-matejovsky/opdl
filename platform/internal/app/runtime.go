package app

import (
	"context"
	"errors"
	"fmt"
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
		// The fence is released only after the active resources have closed, so no
		// second process can open the same listener or server while this one is up.
		return errors.Join(runActive(ctx, cfg, descriptor, role, statusPath), fence.Release())
	}
	if !descriptor.Instances.WarmStandby {
		return fmt.Errorf("another process already holds the active fence for machine %q and this machine does not run a warm standby", descriptor.Machine)
	}
	return runStandby(ctx, cfg, descriptor, role, statusPath)
}

// runActive brings this node's Event Fabric up to readiness and serves the public
// API until signaled or until its projection falls too far behind the journal.
func runActive(ctx context.Context, cfg *config.Config, descriptor deployment.Descriptor, role redundancy.ProcessRole, statusPath string) error {
	site, err := open(ctx, descriptor, cfg, true)
	if err != nil {
		return errors.Join(err, writeFailedStatus(statusPath, role, err))
	}

	// A projection that falls too far behind stops serving rather than answering
	// from a stale view: the status loop cancels serving when it crosses the bound.
	serveCtx, stopServing := context.WithCancel(ctx)
	defer stopServing()
	statusDone, err := startStatus(serveCtx, site.fabric, role, redundancy.StateActive, statusPath, cfg.LagBound(), stopServing, stopServing)
	if err != nil {
		return errors.Join(err, site.close(ctx), writeFailedStatus(statusPath, role, err))
	}

	addr := cfg.Address()
	fmt.Printf("platform: %s active, listening on %s\n", role, addr)
	srv := &http.Server{
		Addr:              addr,
		Handler:           httpapi.NewHandler(site.commands, site.queries),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout(),
	}
	serveErr := serve(serveCtx, srv, site, cfg.ShutdownTimeout())
	stopServing()
	statusErr := <-statusDone
	if statusErr != nil {
		return errors.Join(serveErr, statusErr, writeFailedStatus(statusPath, role, statusErr))
	}
	return serveErr
}

// runStandby follows the journal as a warm standby until signaled. It opens a
// client-only transport and the projector, catches up, and keeps its projection
// current, holding no active capability.
func runStandby(ctx context.Context, cfg *config.Config, descriptor deployment.Descriptor, role redundancy.ProcessRole, statusPath string) error {
	site, err := open(ctx, descriptor, cfg, false)
	if err != nil {
		return errors.Join(err, writeFailedStatus(statusPath, role, err))
	}

	// A standby never stops serving on lag because it serves nothing; it only
	// records whether it is current enough to be promoted, so onLagExceeded is nil.
	standbyCtx, stopStandby := context.WithCancel(ctx)
	defer stopStandby()
	statusDone, err := startStatus(standbyCtx, site.fabric, role, redundancy.StateStandby, statusPath, cfg.LagBound(), nil, stopStandby)
	if err != nil {
		return errors.Join(err, site.close(ctx), writeFailedStatus(statusPath, role, err))
	}

	fmt.Printf("platform: %s following the journal\n", role)
	standbyErr := serveStandby(standbyCtx, site)
	stopStandby()
	statusErr := <-statusDone
	if statusErr != nil {
		return errors.Join(standbyErr, statusErr, writeFailedStatus(statusPath, role, statusErr))
	}
	return standbyErr
}

// serveStandby follows the journal until ctx is canceled or the projector stops,
// then releases the standby. A standby has no listener to stop; it exists to keep
// its projection current, so its projector giving up is the only thing besides a
// signal that ends it. Canceling a standby ends it without promotion.
func serveStandby(ctx context.Context, site *site) error {
	select {
	case <-ctx.Done():
	case <-site.stopped:
		fmt.Fprintln(os.Stderr, "platform: the standby projector stopped following the journal; shutting down")
	}
	return site.close(ctx)
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
			status.Lag = "unknown"
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
