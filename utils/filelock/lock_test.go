package filelock_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/miroslav-matejovsky/opdl/utils/filelock"
	"github.com/stretchr/testify/require"
)

func TestLockLifecycleAndIdempotency(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.lock")

	lock, err := filelock.Open(path)
	require.NoError(t, err)
	require.False(t, lock.Held())
	require.Equal(t, path, lock.Path())

	acquired, err := lock.TryAcquire()
	require.NoError(t, err)
	require.True(t, acquired)
	require.True(t, lock.Held())

	acquired, err = lock.TryAcquire()
	require.NoError(t, err)
	require.True(t, acquired)

	require.NoError(t, lock.Release())
	require.False(t, lock.Held())

	require.NoError(t, lock.Release())
	require.False(t, lock.Held())

	acquired, err = lock.TryAcquire()
	require.NoError(t, err)
	require.True(t, acquired)
	require.NoError(t, lock.Release())
}

func TestTwoLocksInOneProcessAreExclusive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "exclusive.lock")

	l1, err := filelock.Open(path)
	require.NoError(t, err)
	l2, err := filelock.Open(path)
	require.NoError(t, err)

	acq1, err := l1.TryAcquire()
	require.NoError(t, err)
	require.True(t, acq1)

	acq2, err := l2.TryAcquire()
	require.NoError(t, err)
	require.False(t, acq2)

	require.NoError(t, l1.Release())

	acq2, err = l2.TryAcquire()
	require.NoError(t, err)
	require.True(t, acq2)
	require.NoError(t, l2.Release())
}

func TestAcquireContextCancellation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "cancel.lock")

	l1, err := filelock.Open(path)
	require.NoError(t, err)
	acq, err := l1.TryAcquire()
	require.NoError(t, err)
	require.True(t, acq)
	defer func() { _ = l1.Release() }()

	l2, err := filelock.Open(path)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	err = l2.Acquire(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, l2.Held())
}

func TestOpenAndLockErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "dirAsFile.lock")
	require.NoError(t, os.MkdirAll(path, 0o755))

	lock, err := filelock.Open(path)
	require.NoError(t, err)

	_, err = lock.TryAcquire()
	require.Error(t, err)
	require.Contains(t, err.Error(), "filelock: open")
	require.Contains(t, err.Error(), path)
}

func TestSeparateProcessesAndCrashRelease(t *testing.T) {
	if os.Getenv("FILELOCK_HELPER_PROCESS") == "1" {
		path := os.Getenv("FILELOCK_HELPER_PATH")
		lock, err := filelock.Open(path)
		if err != nil {
			os.Exit(1)
		}
		acq, err := lock.TryAcquire()
		if err != nil || !acq {
			os.Exit(2)
		}
		_, _ = os.Stdout.WriteString("ready\n")
		buf := make([]byte, 1)
		_, _ = os.Stdin.Read(buf)
		if os.Getenv("FILELOCK_HELPER_MODE") == "release" {
			_ = lock.Release()
		}
		os.Exit(0)
	}

	t.Parallel()

	t.Run("graceful release across processes", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "graceful.lock")

		cmd, stdin, stdout := startHelper(t, path, "release")
		defer cleanupCmd(cmd)

		waitReady(t, stdout)

		l2, err := filelock.Open(path)
		require.NoError(t, err)
		acq, err := l2.TryAcquire()
		require.NoError(t, err)
		require.False(t, acq, "helper process currently holds the lock")

		_ = stdin.Close()
		require.NoError(t, cmd.Wait())

		require.NoError(t, l2.Acquire(t.Context()))
		require.NoError(t, l2.Release())
	})

	t.Run("forced death releases lock across processes", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "crash.lock")

		cmd, _, stdout := startHelper(t, path, "kill")
		defer cleanupCmd(cmd)

		waitReady(t, stdout)

		l2, err := filelock.Open(path)
		require.NoError(t, err)
		acq, err := l2.TryAcquire()
		require.NoError(t, err)
		require.False(t, acq)

		_ = cmd.Process.Kill()
		_ = cmd.Wait()

		require.NoError(t, l2.Acquire(t.Context()))
		require.NoError(t, l2.Release())
	})
}

func startHelper(t *testing.T, path, mode string) (cmd *exec.Cmd, stdinW, stdoutR *os.File) {
	t.Helper()
	cmd = exec.CommandContext(t.Context(), os.Args[0], "-test.run=TestSeparateProcessesAndCrashRelease")
	cmd.Env = append(os.Environ(),
		"FILELOCK_HELPER_PROCESS=1",
		"FILELOCK_HELPER_PATH="+path,
		"FILELOCK_HELPER_MODE="+mode,
	)
	stdinR, stdinW, err := os.Pipe()
	require.NoError(t, err)
	stdoutR, stdoutW, err := os.Pipe()
	require.NoError(t, err)

	cmd.Stdin = stdinR
	cmd.Stdout = stdoutW
	require.NoError(t, cmd.Start())

	_ = stdinR.Close()
	_ = stdoutW.Close()
	return cmd, stdinW, stdoutR
}

func waitReady(t *testing.T, stdout *os.File) {
	t.Helper()
	buf := make([]byte, 6)
	_, err := stdout.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "ready\n", string(buf))
}

func cleanupCmd(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
}
