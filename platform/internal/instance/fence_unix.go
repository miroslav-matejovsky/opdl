//go:build !windows

package instance

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// tryLock takes an exclusive, non-blocking advisory lock on file via flock. It
// reports false when another open file description already holds the lock, and an
// error only for an unexpected failure. The lock is released when the descriptor
// closes, including on process exit, which is what makes the fence non-expiring
// and crash-safe.
func tryLock(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, unix.EWOULDBLOCK) {
		return false, nil
	}
	return false, err
}

// unlock releases the flock lock tryLock took on file.
func unlock(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
