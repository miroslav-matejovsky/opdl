package instance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FenceFileName is the name of the lock file both slots on one machine contend
// for under the machine's local runtime directory.
const FenceFileName = "active.lock"

// fencePollInterval is how often Acquire retries a held fence while it waits. The
// OS lock is non-blocking, so Acquire polls; the interval trades a small promotion
// delay against idle wakeups while a standby waits.
const fencePollInterval = 100 * time.Millisecond

// FencePath returns the machine fence's lock-file path under runtimeDir. The path
// is derived from the deployment identity — project, environment, site, and
// machine — and deliberately does not include the slot: both slots of a machine
// must contend for the same lock, so the slot must never change the path.
//
// The identity is joined into one machine-specific subdirectory shared by the two
// slots. runtimeDir is a local configuration value the composer supplies; it must
// be on a local filesystem, because the fence relies on the lock being dropped on
// process death and enforced exclusively, which a network filesystem may not do.
func FencePath(runtimeDir, project, environment, site, machine string) string {
	machineDir := strings.Join([]string{project, environment, site, machine}, "-")
	return filepath.Join(runtimeDir, machineDir, FenceFileName)
}

// Fence is one machine's exclusive active lock, held through an OS file lock in
// the machine's local runtime directory. It is the only thing that decides which
// of a machine's two slots owns the active, externally visible, decision-producing
// capabilities.
//
// The lock is exclusive and non-expiring: exactly one slot holds it, and it is
// released only when the holder releases it or the holding process exits. A crash
// releases it automatically, because the operating system drops the lock when the
// file handle closes; a clean stop releases it explicitly. There is no lease and
// no timeout, so a paused holder keeps the fence until its service manager
// terminates it and can never wake into a second active.
//
// A Fence is safe for concurrent use.
type Fence struct {
	path string
	slot Slot

	mu   sync.Mutex
	file *os.File // non-nil while this slot holds the fence
}

// OpenFence prepares slot's contender for the machine fence at path. It creates
// the runtime directory but does not take the lock; call Acquire to contend for
// it. path is the same for both slots of a machine, so slot is carried only for
// diagnostics.
func OpenFence(path string, slot Slot) (*Fence, error) {
	if !slot.Valid() {
		return nil, fmt.Errorf("instance: open fence: invalid slot %q", slot)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("instance: create runtime directory: %w", err)
	}
	return &Fence{path: path, slot: slot}, nil
}

// Acquire blocks until this slot holds the machine fence or ctx is canceled. It
// tries the OS lock immediately and, while another slot holds it, retries until
// the holder releases it or exits, or until ctx is done. Canceling a waiting
// standby ends the wait without acquiring and without promotion.
//
// Acquire returning is not the moment a slot becomes active. The caller composes
// its active resources after Acquire returns, serves only then, and must Release
// after those resources have closed. Acquire is idempotent for a slot that
// already holds the fence.
func (f *Fence) Acquire(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		acquired, err := f.tryAcquire()
		if err != nil {
			return err
		}
		if acquired {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(fencePollInterval):
		}
	}
}

// tryAcquire takes the OS lock once without blocking. It reports whether this
// slot now holds the fence, and an error only for an unexpected failure, not for
// a fence another slot legitimately holds.
func (f *Fence) tryAcquire() (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.file != nil {
		return true, nil
	}
	file, err := os.OpenFile(f.path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false, fmt.Errorf("instance: open fence file %s: %w", f.path, err)
	}
	locked, err := tryLock(file)
	if err != nil {
		_ = file.Close()
		return false, fmt.Errorf("instance: lock fence %s: %w", f.path, err)
	}
	if !locked {
		_ = file.Close()
		return false, nil
	}
	f.file = file
	return true, nil
}

// Release drops the machine fence so another slot may become active. It must be
// called only after the caller's active resources have closed: releasing while a
// listener, handler, or embedded server is still up would let a second slot open
// the same resources and overlap. Release is idempotent.
func (f *Fence) Release() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.file == nil {
		return nil
	}
	unlockErr := unlock(f.file)
	closeErr := f.file.Close()
	f.file = nil
	if unlockErr != nil {
		return fmt.Errorf("instance: release fence %s: %w", f.path, unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("instance: close fence file %s: %w", f.path, closeErr)
	}
	return nil
}

// Held reports whether this slot currently holds the machine fence.
func (f *Fence) Held() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.file != nil
}

// Path returns the fence's lock-file path.
func (f *Fence) Path() string { return f.path }

// Slot returns the slot this fence contends for.
func (f *Fence) Slot() Slot { return f.slot }
