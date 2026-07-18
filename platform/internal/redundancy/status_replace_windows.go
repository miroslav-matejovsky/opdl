//go:build windows

package redundancy

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

const (
	statusReplaceRetry   = 10 * time.Millisecond
	statusReplaceTimeout = time.Second
)

// replaceStatusFile tolerates a reader's short-lived Windows sharing lock. The
// replacement remains bounded, so a persistent filesystem failure still stops
// the runtime instead of leaving deployment tooling with stale status.
func replaceStatusFile(source, target string) error {
	deadline := time.Now().Add(statusReplaceTimeout)
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
		time.Sleep(statusReplaceRetry)
	}
}
