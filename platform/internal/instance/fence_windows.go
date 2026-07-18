//go:build windows

package instance

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLock takes an exclusive, non-blocking lock on file. It reports false when
// another handle already holds the lock, and an error only for an unexpected
// failure. The lock is released when the handle closes, including on process
// exit, which is what makes the fence non-expiring and crash-safe.
func tryLock(file *os.File) (bool, error) {
	var overlapped windows.Overlapped
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &overlapped,
	)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return false, err
}

// unlock releases the single-byte lock tryLock took on file.
func unlock(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
}
