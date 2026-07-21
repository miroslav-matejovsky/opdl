package atomicfile_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miroslav-matejovsky/opdl/utils/atomicfile"
	"github.com/stretchr/testify/require"
)

func TestWriteFileRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
		perm os.FileMode
	}{
		{name: "empty", data: []byte{}, perm: 0o600},
		{name: "simple text", data: []byte("hello, atomic world\n"), perm: 0o644},
		{name: "binary data", data: []byte{0x00, 0xff, 0x10, 0x20, 0x0a, 0x00}, perm: 0o755},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, tc.name+".txt")
			require.NoError(t, atomicfile.WriteFile(path, tc.data, tc.perm))

			got, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, tc.data, got)

			info, err := os.Stat(path)
			require.NoError(t, err)
			require.Equal(t, tc.perm&0o222 == 0, info.Mode()&0o222 == 0)
			checkNoTempFiles(t, dir)
		})
	}
}

func TestWriteFileConcurrentReadsAndWritesNoPartialContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "shared.txt")

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()

	payload1 := []byte("first-complete-payload-content")
	payload2 := []byte("second-complete-payload-longer-content")

	require.NoError(t, atomicfile.WriteFile(path, payload1, 0o644))

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		i := 0
		for ctx.Err() == nil {
			payload := payload1
			if i%2 == 1 {
				payload = payload2
			}
			require.NoError(t, atomicfile.WriteFile(path, payload, 0o644))
			i++
		}
	}()

	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			if !bytes.Equal(data, payload1) && !bytes.Equal(data, payload2) {
				t.Errorf("observed partial or corrupted content (%d bytes): %q", len(data), data)
				return
			}
		}
	}()

	wg.Wait()
	checkNoTempFiles(t, dir)
}

func TestWriteFileWindowsReplacementSucceedsAfterReaderReleasesHandle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.txt")

	require.NoError(t, atomicfile.WriteFile(path, []byte("initial"), 0o644))

	reader, err := os.Open(path)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		done <- atomicfile.WriteFile(path, []byte("updated"), 0o644)
	}()

	time.Sleep(20 * time.Millisecond)
	require.NoError(t, reader.Close())
	require.NoError(t, <-done)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, []byte("updated"), got)
	checkNoTempFiles(t, dir)
}

func TestWriteFileFailuresAndCleanup(t *testing.T) {
	t.Parallel()

	t.Run("create failure", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "missing-subdir", "file.txt")

		err := atomicfile.WriteFile(path, []byte("data"), 0o644)
		require.Error(t, err)
		require.Contains(t, err.Error(), "atomicfile: create")
		require.Contains(t, err.Error(), path)
		checkNoTempFiles(t, dir)
	})

	t.Run("replace failure due to existing directory target", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "dir-target")
		require.NoError(t, os.Mkdir(path, 0o755))

		err := atomicfile.WriteFile(path, []byte("data"), 0o644)
		require.Error(t, err)
		require.Contains(t, err.Error(), "atomicfile: replace")
		require.Contains(t, err.Error(), path)
		checkNoTempFiles(t, dir)
	})
}

func TestWriteFileConcurrentWritersDistinctPathsAndCleanup(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "target.txt")

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func(id int) {
			defer wg.Done()
			data := []byte(fmt.Sprintf("writer-%d", id))
			require.NoError(t, atomicfile.WriteFile(path, data, 0o644))
		}(i)
	}

	wg.Wait()
	require.FileExists(t, path)
	checkNoTempFiles(t, dir)
}

func checkNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, entry := range entries {
		require.False(t, strings.HasSuffix(entry.Name(), ".tmp"), "found leaked temporary file: %s", entry.Name())
	}
}
