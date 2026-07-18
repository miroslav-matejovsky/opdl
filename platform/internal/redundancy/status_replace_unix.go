//go:build !windows

package redundancy

import "os"

func replaceStatusFile(source, target string) error {
	return os.Rename(source, target)
}
