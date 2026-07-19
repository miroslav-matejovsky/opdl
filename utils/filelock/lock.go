package filelock

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const pollInterval = 100 * time.Millisecond

// Lock represents an exclusive local file lock.
type Lock struct {
	path string

	mu   sync.Mutex
	file *os.File
}

// Open prepares a file lock at path. It creates the parent directory if necessary
// but does not acquire the lock until TryAcquire or Acquire is called.
func Open(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("filelock: create parent directory: %w", err)
	}
	return &Lock{path: path}, nil
}

// TryAcquire attempts to acquire the lock without blocking. It returns true if the
// lock was acquired, false if it is currently held by another process or description,
// and an error only if an unexpected operation failure occurred.
func (l *Lock) TryAcquire() (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		return true, nil
	}
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false, fmt.Errorf("filelock: open %s: %w", l.path, err)
	}
	locked, err := tryLock(file)
	if err != nil {
		_ = file.Close()
		return false, fmt.Errorf("filelock: lock %s: %w", l.path, err)
	}
	if !locked {
		_ = file.Close()
		return false, nil
	}
	l.file = file
	return true, nil
}

// Acquire blocks until the lock is acquired or ctx is canceled.
func (l *Lock) Acquire(ctx context.Context) error {
	timer := time.NewTimer(pollInterval)
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	defer timer.Stop()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		acquired, err := l.TryAcquire()
		if err != nil {
			return err
		}
		if acquired {
			return nil
		}
		timer.Reset(pollInterval)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Release drops the lock if currently held. Releasing is idempotent and safe for
// concurrent calls.
func (l *Lock) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	unlockErr := unlock(l.file)
	closeErr := l.file.Close()
	l.file = nil
	if unlockErr != nil {
		return fmt.Errorf("filelock: unlock %s: %w", l.path, unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("filelock: close %s: %w", l.path, closeErr)
	}
	return nil
}

// Held returns true if this Lock instance currently holds the file lock.
func (l *Lock) Held() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file != nil
}

// Path returns the path of the lock file.
func (l *Lock) Path() string { return l.path }
