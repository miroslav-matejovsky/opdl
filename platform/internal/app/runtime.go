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

// statusInterval is how often a running slot rewrites its local status file. It
// is short enough to be useful to a watching deployment tool and long enough not
// to churn the disk.
const statusInterval = time.Second

// resolveSlot decides which local slot this process runs as, from the -instance
// argument and the machine's warm-standby policy.
//
// A machine that runs a warm standby requires an explicit slot, so two service
// definitions can never share one local identity. A machine that opted out
// defaults to slot a and rejects slot b, because it has no second slot to run.
func resolveSlot(instance string, warmStandby bool) (redundancy.Slot, error) {
	if instance == "" {
		if warmStandby {
			return "", fmt.Errorf("this machine runs a warm standby: -instance a|b is required")
		}
		return redundancy.SlotA, nil
	}
	slot, err := redundancy.ParseSlot(instance)
	if err != nil {
		return "", err
	}
	if !warmStandby && slot == redundancy.SlotB {
		return "", fmt.Errorf("this machine does not run a warm standby: only -instance a is valid")
	}
	return slot, nil
}

// runSlot runs this process as its slot: it contends for the machine fence, and
// runs the active runtime if it wins the fence or a warm standby if another slot
// already holds it.
//
// The fence is what makes the two slots exclusive. The slot that holds it owns
// every active-only capability; a slot that finds it held follows the journal as
// a standby. A machine that opted out of warm standby refuses to start a second
// slot at all, so a stray launch fails loudly rather than running unnoticed.
func runSlot(ctx context.Context, cfg *config.Config, descriptor deployment.Descriptor, slot redundancy.Slot) error {
	fencePath := redundancy.FencePath(cfg.InstanceDir(), descriptor.Project, descriptor.Environment, descriptor.Site, descriptor.Machine)
	statusPath := redundancy.StatusPath(cfg.InstanceDir(), descriptor.Project, descriptor.Environment, descriptor.Site, descriptor.Machine, slot)

	fence, err := redundancy.OpenFence(fencePath, slot)
	if err != nil {
		return err
	}

	acquired, err := fence.TryAcquire()
	if err != nil {
		return err
	}
	if acquired {
		// The fence is released only after the active resources have closed, so no
		// second slot can open the same listener or server while this one is up.
		return errors.Join(runActive(ctx, cfg, descriptor, slot, statusPath), fence.Release())
	}
	if !descriptor.Instances.WarmStandby {
		return fmt.Errorf("another slot already holds the active fence for machine %q and this machine does not run a warm standby", descriptor.Machine)
	}
	return runStandby(ctx, cfg, descriptor, slot, statusPath)
}

// runActive brings this node's Event Fabric up to readiness and serves the public
// API until signaled or until its projection falls too far behind the journal.
func runActive(ctx context.Context, cfg *config.Config, descriptor deployment.Descriptor, slot redundancy.Slot, statusPath string) error {
	site, err := open(ctx, descriptor, cfg, true)
	if err != nil {
		return errors.Join(err, writeFailedStatus(statusPath, slot, err))
	}

	// A projection that falls too far behind stops serving rather than answering
	// from a stale view: the status loop cancels serving when it crosses the bound.
	serveCtx, stopServing := context.WithCancel(ctx)
	defer stopServing()
	statusDone, err := startStatus(serveCtx, site.fabric, slot, redundancy.StateActive, statusPath, cfg.LagBound(), stopServing, stopServing)
	if err != nil {
		return errors.Join(err, site.close(ctx), writeFailedStatus(statusPath, slot, err))
	}

	addr := cfg.Address()
	fmt.Printf("platform: slot %s active, listening on %s\n", slot, addr)
	srv := &http.Server{
		Addr:              addr,
		Handler:           httpapi.NewHandler(site.commands, site.queries),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout(),
	}
	serveErr := serve(serveCtx, srv, site, cfg.ShutdownTimeout())
	stopServing()
	statusErr := <-statusDone
	if statusErr != nil {
		return errors.Join(serveErr, statusErr, writeFailedStatus(statusPath, slot, statusErr))
	}
	return serveErr
}

// runStandby follows the journal as a warm standby until signaled. It opens a
// client-only transport and the projector, catches up, and keeps its projection
// current, holding no active capability.
func runStandby(ctx context.Context, cfg *config.Config, descriptor deployment.Descriptor, slot redundancy.Slot, statusPath string) error {
	site, err := open(ctx, descriptor, cfg, false)
	if err != nil {
		return errors.Join(err, writeFailedStatus(statusPath, slot, err))
	}

	// A standby never stops serving on lag because it serves nothing; it only
	// records whether it is current enough to be promoted, so onLagExceeded is nil.
	standbyCtx, stopStandby := context.WithCancel(ctx)
	defer stopStandby()
	statusDone, err := startStatus(standbyCtx, site.fabric, slot, redundancy.StateStandby, statusPath, cfg.LagBound(), nil, stopStandby)
	if err != nil {
		return errors.Join(err, site.close(ctx), writeFailedStatus(statusPath, slot, err))
	}

	fmt.Printf("platform: slot %s warm standby, following the journal\n", slot)
	standbyErr := serveStandby(standbyCtx, site)
	stopStandby()
	statusErr := <-statusDone
	if statusErr != nil {
		return errors.Join(standbyErr, statusErr, writeFailedStatus(statusPath, slot, statusErr))
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
func startStatus(ctx context.Context, fabric statusFabric, slot redundancy.Slot, state redundancy.State, statusPath string, lagBound time.Duration, onLagExceeded, onFailure func()) (<-chan error, error) {
	var lag redundancy.LagState
	write := func() error {
		now := time.Now()
		status := redundancy.Status{Slot: slot, State: state, PID: os.Getpid(), UpdatedAt: now.UTC(), Promotable: true}
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

// writeFailedStatus records that a slot could not open so a stopped slot leaves
// a reason behind rather than a stale or missing file.
func writeFailedStatus(statusPath string, slot redundancy.Slot, cause error) error {
	return redundancy.Status{
		Slot:      slot,
		State:     redundancy.StateFailed,
		PID:       os.Getpid(),
		UpdatedAt: time.Now().UTC(),
		LastError: cause.Error(),
	}.Write(statusPath)
}
