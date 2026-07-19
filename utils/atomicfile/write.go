package atomicfile

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
)

var tempSeq uint64

// WriteFile writes data to path atomically with the requested permissions perm.
//
// It creates a unique sibling temporary file in the directory of path, writes
// data to it, closes it, and renames it over path. Readers observing path will
// see either the previous complete file or the new complete file, never partial
// content. If any error occurs during file creation, writing, closing, or
// replacing, any leftover temporary file is removed immediately.
//
// On Windows, short-lived sharing violations during replacement are retried for
// up to one second before failing. All errors returned retain the failing path
// and operation.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	tmpPath, f, err := createTempFile(dir, base, perm)
	if err != nil {
		return fmt.Errorf("atomicfile: create %s: %w", path, err)
	}

	var removed bool
	defer func() {
		if !removed {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("atomicfile: write %s: %w", tmpPath, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("atomicfile: close %s: %w", tmpPath, err)
	}
	if err := replaceFile(tmpPath, path); err != nil {
		return fmt.Errorf("atomicfile: replace %s: %w", path, err)
	}
	removed = true
	return nil
}

// createTempFile opens a unique temporary file in dir with the given base
// name prefix and requested permissions.
func createTempFile(dir, base string, perm os.FileMode) (string, *os.File, error) {
	pid := os.Getpid()
	for i := 0; i < 10000; i++ {
		seq := atomic.AddUint64(&tempSeq, 1)
		var randBuf [4]byte
		_, _ = rand.Read(randBuf[:])
		name := fmt.Sprintf(".%s.%d.%d.%x.tmp", base, pid, seq, randBuf)
		tmpPath := filepath.Join(dir, name)

		f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return tmpPath, nil, err
		}
		return tmpPath, f, nil
	}
	return "", nil, fmt.Errorf("exhausted temporary file attempts in %s", dir)
}
