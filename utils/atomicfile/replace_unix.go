//go:build !windows

package atomicfile

import "os"

// replaceFile atomically replaces target with source using os.Rename.
func replaceFile(source, target string) error {
	return os.Rename(source, target)
}
