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

// StatusPath returns an instance's status-file path under runtimeDir.
//
// Status files are operational evidence only. Primary Ownership decides which
// instance is Active, so runtimeDir takes no part in that decision and may be
// moved without affecting it.
func StatusPath(runtimeDir, project, environment, site, machine string, role InstanceRole) string {
	return filepath.Join(machineDir(runtimeDir, project, environment, site, machine), "process-"+string(role)+".status")
}

// Acquisition is the outcome of contending for Primary Ownership.
type Acquisition struct {
	// Held reports whether this process now holds Primary Ownership.
	Held bool
	// Abandoned reports that ownership was taken over from a process that died
	// without releasing it, rather than from one that handed it over cleanly. It
	// is only meaningful when Held is true.
	//
	// Ownership is equally valid either way: a dead process's listener, handlers,
	// and embedded server died with it. The distinction is operational, and it is
	// one the file lock this replaced could not make at all.
	Abandoned bool
}

// Ownership is a machine's Primary Ownership, held through a Windows named mutex.
// Holding it is what makes an instance Active; nothing else does.
//
// Exactly one process holds it, and only the holder releasing it or exiting frees
// it. A crash frees it automatically, because the kernel abandons a mutex whose
// owning process is gone. There is no lease and no timeout, so a paused holder
// keeps ownership until its service manager terminates it and can never wake into
// a second Active instance.
//
// See the package documentation for why ownership is a kernel object rather than
// a lock file, and for the release ordering the shared endpoints depend on.
//
// An Ownership is safe for concurrent use.
type Ownership struct {
	mutex *winmutex.Mutex
	role  InstanceRole
}

// OpenOwnership prepares this instance to contend for the ownership object named
// by object, which comes from the machine's deployment descriptor. It opens or
// creates the object but does not take it; call TryAcquire or Acquire to contend.
//
// object is the same for both instances of a machine, so role is carried only for
// diagnostics.
func OpenOwnership(object string, role InstanceRole) (*Ownership, error) {
	if !role.Valid() {
		return nil, fmt.Errorf("redundancy: open ownership: invalid instance role %q", role)
	}
	if strings.TrimSpace(object) == "" {
		return nil, fmt.Errorf("redundancy: open ownership: descriptor carries no ownership object")
	}
	mutex, err := winmutex.Open(object)
	if err != nil {
		return nil, fmt.Errorf("redundancy: open ownership object: %w", err)
	}
	return &Ownership{mutex: mutex, role: role}, nil
}

// TryAcquire takes Primary Ownership without blocking. It reports the outcome,
// and an error only for an unexpected failure, never for ownership the other
// instance legitimately holds.
//
// The instance that takes it becomes Active. The other remains Standby.
func (o *Ownership) TryAcquire() (Acquisition, error) {
	outcome, err := o.mutex.TryAcquire()
	if err != nil {
		return Acquisition{}, fmt.Errorf("redundancy: acquire ownership %s: %w", o.mutex.Name(), err)
	}
	return acquisition(outcome), nil
}

// Acquire blocks until this instance holds Primary Ownership or ctx is canceled.
//
// The wait is a kernel wait: the waiter is woken the moment the holder releases or
// dies, with no polling interval in between. Canceling ends the wait without
// acquiring and without becoming Active.
//
// Acquire returning is not the moment an instance becomes Active. The caller
// composes its active resources after Acquire returns, serves only then, and must
// Release after those resources have closed.
func (o *Ownership) Acquire(ctx context.Context) (Acquisition, error) {
	outcome, err := o.mutex.Acquire(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return Acquisition{}, ctx.Err()
		}
		return Acquisition{}, fmt.Errorf("redundancy: acquire ownership %s: %w", o.mutex.Name(), err)
	}
	return acquisition(outcome), nil
}

func acquisition(outcome winmutex.Outcome) Acquisition {
	return Acquisition{Held: outcome.Held(), Abandoned: outcome == winmutex.AcquiredAbandoned}
}

// Release drops Primary Ownership so the other instance may become Active. It must
// be called only after the caller's active resources have closed: releasing while a
// listener, handler, or embedded server is still up would let a second process open
// the same resources and overlap. Release is idempotent.
func (o *Ownership) Release() error {
	if err := o.mutex.Release(); err != nil {
		return fmt.Errorf("redundancy: release ownership %s: %w", o.mutex.Name(), err)
	}
	return nil
}

// Close releases the object's kernel handles and stops its owner thread. It is
// idempotent, and it releases ownership first if the caller still holds it, so an
// instance that closes without releasing hands over cleanly rather than appearing
// to have crashed.
func (o *Ownership) Close() error {
	if err := o.mutex.Close(); err != nil {
		return fmt.Errorf("redundancy: close ownership %s: %w", o.mutex.Name(), err)
	}
	return nil
}

// Held reports whether this instance currently holds Primary Ownership.
func (o *Ownership) Held() bool { return o.mutex.Held() }

// Name returns the fully qualified ownership object name. It is what an operator
// needs to identify the object, which unlike a lock file has no path.
func (o *Ownership) Name() string { return o.mutex.Name() }

// Existed reports whether the ownership object already existed when this process
// opened it, meaning a peer process on this machine is running.
//
// It is a diagnostic, not a fault. On a machine deploying both a primary and a
// standby, the second to start legitimately finds the object. A process that
// expects a peer and does not find one, or finds one on a machine deployed
// without a standby, is worth an operator's attention.
func (o *Ownership) Existed() bool { return o.mutex.Existed() }

// Role returns the instance role contending for ownership.
func (o *Ownership) Role() InstanceRole { return o.role }
