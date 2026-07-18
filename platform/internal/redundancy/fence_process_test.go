package redundancy_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/miroslav-matejovsky/opdl/platform/internal/redundancy"
	"github.com/stretchr/testify/require"
)

const (
	fenceHelperMode = "OPDL_FENCE_HELPER_MODE"
	fenceHelperPath = "OPDL_FENCE_HELPER_PATH"
)

// TestFenceProcessHelper is re-executed by the subprocess tests below. Stdin is
// the deterministic service-manager control channel: EOF releases a holder or
// cancels a waiter.
func TestFenceProcessHelper(t *testing.T) {
	mode := os.Getenv(fenceHelperMode)
	if mode == "" {
		return
	}
	fence, err := redundancy.OpenFence(os.Getenv(fenceHelperPath), redundancy.RoleStandby)
	require.NoError(t, err)

	switch mode {
	case "hold":
		require.NoError(t, fence.Acquire(t.Context()))
		fmt.Println("held")
		_, _ = io.Copy(io.Discard, os.Stdin)
		require.NoError(t, fence.Release())
	case "wait":
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			cancel()
		}()
		fmt.Println("waiting")
		require.ErrorIs(t, fence.Acquire(ctx), context.Canceled)
		fmt.Println("cancelled")
	default:
		require.FailNowf(t, "unknown helper mode", "%q", mode)
	}
}

func TestFenceReleasesAcrossProcessesAfterGracefulClose(t *testing.T) {
	path := redundancy.FencePath(t.TempDir(), "p", "e", "s", "m")
	holder := startFenceHelper(t, path, "hold", "held")

	contender, err := redundancy.OpenFence(path, redundancy.RolePrimary)
	require.NoError(t, err)
	acquired, err := contender.TryAcquire()
	require.NoError(t, err)
	require.False(t, acquired, "two processes held the fence")

	holder.closeInput(t)
	holder.wait(t)
	require.NoError(t, contender.Acquire(t.Context()))
	require.NoError(t, contender.Release())
}

func TestFenceReleasesAcrossProcessesAfterForcedDeath(t *testing.T) {
	path := redundancy.FencePath(t.TempDir(), "p", "e", "s", "m")
	holder := startFenceHelper(t, path, "hold", "held")
	holder.kill(t)

	contender, err := redundancy.OpenFence(path, redundancy.RolePrimary)
	require.NoError(t, err)
	require.NoError(t, contender.Acquire(t.Context()))
	require.NoError(t, contender.Release())
}

func TestFenceWaitCancellationAcrossProcesses(t *testing.T) {
	path := redundancy.FencePath(t.TempDir(), "p", "e", "s", "m")
	holder := startFenceHelper(t, path, "hold", "held")
	waiter := startFenceHelper(t, path, "wait", "waiting")

	waiter.closeInput(t)
	require.Equal(t, "cancelled", waiter.readLine(t))
	waiter.wait(t)

	contender, err := redundancy.OpenFence(path, redundancy.RolePrimary)
	require.NoError(t, err)
	acquired, err := contender.TryAcquire()
	require.NoError(t, err)
	require.False(t, acquired, "canceling the waiter disturbed the holder")

	holder.closeInput(t)
	holder.wait(t)
}

type fenceHelper struct {
	command *exec.Cmd
	input   io.WriteCloser
	output  *bufio.Reader
	waited  bool
}

func startFenceHelper(t *testing.T, path, mode, ready string) *fenceHelper {
	t.Helper()
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestFenceProcessHelper$")
	command.Env = append(os.Environ(), fenceHelperMode+"="+mode, fenceHelperPath+"="+path)
	input, err := command.StdinPipe()
	require.NoError(t, err)
	stdout, err := command.StdoutPipe()
	require.NoError(t, err)
	command.Stderr = os.Stderr
	require.NoError(t, command.Start())
	helper := &fenceHelper{command: command, input: input, output: bufio.NewReader(stdout)}
	t.Cleanup(func() {
		if helper.waited {
			return
		}
		_ = helper.command.Process.Kill()
		_ = helper.command.Wait()
	})
	require.Equal(t, ready, helper.readLine(t))
	return helper
}

func (h *fenceHelper) readLine(t *testing.T) string {
	t.Helper()
	line, err := h.output.ReadString('\n')
	require.NoError(t, err)
	return strings.TrimSpace(line)
}

func (h *fenceHelper) closeInput(t *testing.T) {
	t.Helper()
	require.NoError(t, h.input.Close())
}

func (h *fenceHelper) wait(t *testing.T) {
	t.Helper()
	err := h.command.Wait()
	h.waited = true
	require.NoError(t, err)
}

func (h *fenceHelper) kill(t *testing.T) {
	t.Helper()
	require.NoError(t, h.command.Process.Kill())
	err := h.command.Wait()
	h.waited = true
	require.Error(t, err)
}
