package redundancy

import (
	"context"
	"fmt"
	"strings"

	"github.com/miroslav-matejovsky/opdl/utils/winmutex"
)

// This file is the lock: the object a machine's two instances contend for, and
// the operations on it. What holding it means at runtime is ownership, and that
// is ownership.go.
//
// The split follows the vocabulary the redundancy plan sets. A lock is
// configuration: it is authored in the blueprint, carried in the deployment
// descriptor, and opened. Ownership is behavior: it is acquired, validated,
// released, and transferred. "The lock is transferred" and "ownership is opened"
// are both wrong and both read fine, which is why the two live apart.

// Lock is a machine's Primary Ownership handle, held through a Windows named mutex.
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
// A Lock is safe for concurrent use.
type Lock struct {
	mutex *winmutex.Mutex
	role  InstanceRole
}

// OpenLock prepares this instance to contend for the ownership object named
// in the machine's deployment descriptor lock block. It opens or creates the
// object but does not take it; call TryAcquire or Acquire to contend.
//
// When windowsMutex is empty (standby is disabled on the machine), OpenLock returns nil, nil,
// and calling TryAcquire or Acquire on that nil Lock immediately succeeds because
// a lone Primary Instance is Active by construction.
func OpenLock(windowsMutex string, role InstanceRole) (*Lock, error) {
	if !role.Valid() {
		return nil, fmt.Errorf("redundancy: open lock: invalid instance role %q", role)
	}
	if windowsMutex == "" {
		return nil, nil //nolint:nilnil // returning nil Lock when standby is disabled is intentional and nil-safe by contract
	}
	if strings.TrimSpace(windowsMutex) == "" {
		return nil, fmt.Errorf("redundancy: open lock: descriptor carries empty windows_mutex")
	}
	mutex, err := winmutex.Open(windowsMutex)
	if err != nil {
		return nil, fmt.Errorf("redundancy: open lock object: %w", err)
	}
	return &Lock{mutex: mutex, role: role}, nil
}

// TryAcquire takes Primary Ownership without blocking. It reports the outcome,
// and an error only for an unexpected failure, never for ownership the other
// instance legitimately holds.
//
// The instance that takes it becomes Active. The other remains Standby.
func (l *Lock) TryAcquire() (Acquisition, error) {
	if l == nil {
		return Acquisition{Held: true, Abandoned: false}, nil
	}
	outcome, err := l.mutex.TryAcquire()
	if err != nil {
		return Acquisition{}, fmt.Errorf("redundancy: acquire ownership %s: %w", l.mutex.Name(), err)
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
func (l *Lock) Acquire(ctx context.Context) (Acquisition, error) {
	if l == nil {
		return Acquisition{Held: true, Abandoned: false}, nil
	}
	outcome, err := l.mutex.Acquire(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return Acquisition{}, ctx.Err()
		}
		return Acquisition{}, fmt.Errorf("redundancy: acquire ownership %s: %w", l.mutex.Name(), err)
	}
	return acquisition(outcome), nil
}

// Release drops Primary Ownership so the other instance may become Active. It must
// be called only after the caller's active resources have closed: releasing while a
// listener, handler, or embedded server is still up would let a second process open
// the same resources and overlap. Release is idempotent.
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	if err := l.mutex.Release(); err != nil {
		return fmt.Errorf("redundancy: release ownership %s: %w", l.mutex.Name(), err)
	}
	return nil
}

// Close releases the object's kernel handles and stops its owner thread. It is
// idempotent, and it releases ownership first if the caller still holds it, so an
// instance that closes without releasing hands over cleanly rather than appearing
// to have crashed.
func (l *Lock) Close() error {
	if l == nil {
		return nil
	}
	if err := l.mutex.Close(); err != nil {
		return fmt.Errorf("redundancy: close ownership %s: %w", l.mutex.Name(), err)
	}
	return nil
}

// Held reports whether this instance currently holds Primary Ownership.
func (l *Lock) Held() bool {
	if l == nil {
		return true
	}
	return l.mutex.Held()
}

// Name returns the fully qualified ownership object name. It is what an operator
// needs to identify the object, which unlike a lock file has no path.
func (l *Lock) Name() string {
	if l == nil {
		return ""
	}
	return l.mutex.Name()
}

// Existed reports whether the ownership object already existed when this process
// opened it, meaning a peer process on this machine is running.
//
// It is a diagnostic, not a fault. On a machine deploying both a primary and a
// standby, the second to start legitimately finds the object. A process that
// expects a peer and does not find one, or finds one on a machine deployed
// without a standby, is worth an operator's attention.
func (l *Lock) Existed() bool {
	if l == nil {
		return false
	}
	return l.mutex.Existed()
}

// Role returns the instance role contending for ownership.
func (l *Lock) Role() InstanceRole {
	if l == nil {
		return RolePrimary
	}
	return l.role
}

// acquisition translates the kernel outcome into the ownership fact it means.
func acquisition(outcome winmutex.Outcome) Acquisition {
	return Acquisition{Held: outcome.Held(), Abandoned: outcome == winmutex.AcquiredAbandoned}
}
