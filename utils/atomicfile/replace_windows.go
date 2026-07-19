//go:build windows

package atomicfile

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

const (
	replaceRetryInterval = 10 * time.Millisecond
	replaceTimeout       = time.Second
)

// replaceFile atomically replaces target with source using os.Rename. On Windows,
// it tolerates a reader's short-lived sharing lock or access denial by retrying
// for up to one second. Other errors or timeouts return immediately.
func replaceFile(source, target string) error {
	deadline := time.Now().Add(replaceTimeout)
	for {
		err := os.Rename(source, target)
		if err == nil {
			return nil
		}
		if !errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return err
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(replaceRetryInterval)
	}
}
