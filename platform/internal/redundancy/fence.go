package redundancy

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/miroslav-matejovsky/opdl/utils/winmutex"
)

func machineDir(runtimeDir, project, environment, site, machine string) string {
	return filepath.Join(runtimeDir, strings.Join([]string{project, environment, site, machine}, "-"))
}

// StatusPath returns the process role's status-file path under runtimeDir.
//
// Status files are operational evidence only. Active ownership comes from the
// machine fence and from nothing else, so runtimeDir is not part of the ownership
// decision and may be moved without affecting it.
func StatusPath(runtimeDir, project, environment, site, machine string, role ProcessRole) string {
	return filepath.Join(machineDir(runtimeDir, project, environment, site, machine), "process-"+string(role)+".status")
}

// Acquisition is the outcome of contending for the machine fence.
type Acquisition struct {
	// Held reports whether this process now owns the fence.
	Held bool
	// Abandoned reports that ownership was taken over from a process that died
	// without releasing it, rather than from one that handed it over cleanly. It
	// is only meaningful when Held is true.
	//
	// Ownership is equally valid either way: a dead process's listener, handlers,
	// and embedded server died with it. The distinction is operational, and it is
	// one the previous file-lock fence could not make at all.
	Abandoned bool
}

// Fence is one machine's exclusive active ownership, held through a Windows named
// mutex. It is the only thing that decides which process owns the active,
// externally visible, decision-producing capabilities.
//
// The mutex is exclusive and non-expiring: exactly one process holds it, and it is
// released only when the holder releases it or the holding process exits. A crash
// releases it automatically, because the kernel abandons a mutex whose owning
// process is gone; a clean stop releases it explicitly. There is no lease and no
// timeout, so a paused holder keeps the fence until its service manager terminates
// it and can never wake into a second active.
//
// # Why a kernel object rather than a lock file
//
// Ownership is identified by a name in a machine-wide namespace derived from the
// machine's compiled deployment identity, not by a filesystem path. Two processes
// therefore exclude each other because they are the same machine, not because they
// were configured with the same directory. The previous file-lock fence could be
// defeated by pointing the two processes at different runtime directories, by
// installing the same package twice under different paths, or by placing the
// runtime directory on a network filesystem, and nothing detected any of the
// three. None of them are expressible now.
//
// Waiting is also a kernel wait rather than a poll, so a standby is woken when the
// holder releases instead of on its next polling interval.
//
// # Fencing scope
//
// The mutex provides mutual exclusion. It is not a fencing token, and it must not
// be documented as one. What makes exclusion sufficient here is two invariants
// around it:
//
//   - Active resources are closed before ownership is released, so a clean release
//     leaves nothing to fence.
//   - Ownership is acquired and released on one pinned OS thread that lives for the
//     process, so ownership cannot be abandoned while the process still holds its
//     resources. See utils/winmutex.
//
// If the first invariant is ever weakened, an explicit monotonic ownership
// generation becomes necessary; the primitive will not substitute for it.
//
// A Fence is safe for concurrent use.
type Fence struct {
	mutex *winmutex.Mutex
	role  ProcessRole
}

// OpenFence prepares a process contender for the machine fence named by object,
// which comes from the machine's deployment descriptor. It opens or creates the
// ownership object but does not take it; call TryAcquire or Acquire to contend.
//
// object is the same for both processes of a machine, so role is carried only for
// diagnostics.
func OpenFence(object string, role ProcessRole) (*Fence, error) {
	if !role.Valid() {
		return nil, fmt.Errorf("redundancy: open fence: invalid process role %q", role)
	}
	if strings.TrimSpace(object) == "" {
		return nil, fmt.Errorf("redundancy: open fence: descriptor carries no fence object")
	}
	mutex, err := winmutex.Open(object)
	if err != nil {
		return nil, fmt.Errorf("redundancy: open machine fence: %w", err)
	}
	return &Fence{mutex: mutex, role: role}, nil
}

// TryAcquire takes the machine fence without blocking. It reports the outcome, and
// an error only for an unexpected failure, not for a fence another process
// legitimately holds.
//
// The process that takes the fence becomes active. The other remains standby.
func (f *Fence) TryAcquire() (Acquisition, error) {
	outcome, err := f.mutex.TryAcquire()
	if err != nil {
		return Acquisition{}, fmt.Errorf("redundancy: acquire fence %s: %w", f.mutex.Name(), err)
	}
	return acquisition(outcome), nil
}

// Acquire blocks until this process holds the machine fence or ctx is canceled.
//
// The wait is a kernel wait: the standby is woken the moment the holder releases
// or dies, with no polling interval in between. Canceling a waiting standby ends
// the wait without acquiring and without promotion.
//
// Acquire returning is not the moment a process becomes active. The caller composes
// its active resources after Acquire returns, serves only then, and must Release
// after those resources have closed.
func (f *Fence) Acquire(ctx context.Context) (Acquisition, error) {
	outcome, err := f.mutex.Acquire(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return Acquisition{}, ctx.Err()
		}
		return Acquisition{}, fmt.Errorf("redundancy: acquire fence %s: %w", f.mutex.Name(), err)
	}
	return acquisition(outcome), nil
}

func acquisition(outcome winmutex.Outcome) Acquisition {
	return Acquisition{Held: outcome.Held(), Abandoned: outcome == winmutex.AcquiredAbandoned}
}

// Release drops the machine fence so another process may become active. It must be
// called only after the caller's active resources have closed: releasing while a
// listener, handler, or embedded server is still up would let a second process open
// the same resources and overlap. Release is idempotent.
func (f *Fence) Release() error {
	if err := f.mutex.Release(); err != nil {
		return fmt.Errorf("redundancy: release fence %s: %w", f.mutex.Name(), err)
	}
	return nil
}

// Close releases the fence's kernel handles and stops its owner thread. It is
// idempotent, and it releases ownership first if the caller still holds it, so a
// process that closes without releasing hands over cleanly rather than appearing
// to have crashed.
func (f *Fence) Close() error {
	if err := f.mutex.Close(); err != nil {
		return fmt.Errorf("redundancy: close fence %s: %w", f.mutex.Name(), err)
	}
	return nil
}

// Held reports whether this process currently holds the machine fence.
func (f *Fence) Held() bool { return f.mutex.Held() }

// Name returns the fence's fully qualified ownership object name. It is what an
// operator needs to identify the object, which unlike a lock file has no path.
func (f *Fence) Name() string { return f.mutex.Name() }

// Existed reports whether the ownership object already existed when this process
// opened it, meaning a peer process on this machine is running.
//
// It is a diagnostic, not a fault. On a machine deploying both a primary and a
// standby, the second to start legitimately finds the object. A process that
// expects a peer and does not find one, or finds one on a machine deployed
// without a standby, is worth an operator's attention.
func (f *Fence) Existed() bool { return f.mutex.Existed() }

// Role returns the process role this fence contends for.
func (f *Fence) Role() ProcessRole { return f.role }
