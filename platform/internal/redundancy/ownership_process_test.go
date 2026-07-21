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
	ownershipHelperMode   = "OPDL_FENCE_HELPER_MODE"
	ownershipHelperObject = "OPDL_FENCE_HELPER_OBJECT"
)

// TestOwnershipProcessHelper is re-executed by the subprocess tests below. Stdin is
// the deterministic service-manager control channel: EOF releases a holder or
// cancels a waiter.
func TestOwnershipProcessHelper(t *testing.T) {
	mode := os.Getenv(ownershipHelperMode)
	if mode == "" {
		return
	}
	ownership, err := redundancy.OpenOwnership(os.Getenv(ownershipHelperObject), redundancy.RoleStandby)
	require.NoError(t, err)

	switch mode {
	case "hold":
		_, err := ownership.Acquire(t.Context())
		require.NoError(t, err)
		fmt.Println("held")
		_, _ = io.Copy(io.Discard, os.Stdin)
		require.NoError(t, ownership.Release())
	case "abandon":
		// Takes the ownership and exits without releasing, so the next holder sees the
		// kernel abandon it rather than a clean handover.
		_, err := ownership.Acquire(t.Context())
		require.NoError(t, err)
		fmt.Println("held")
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	case "wait":
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			cancel()
		}()
		fmt.Println("waiting")
		_, err := ownership.Acquire(ctx)
		require.ErrorIs(t, err, context.Canceled)
		fmt.Println("cancelled")
	default:
		require.FailNowf(t, "unknown helper mode", "%q", mode)
	}
}

func TestOwnershipReleasesAcrossProcessesAfterGracefulClose(t *testing.T) {
	object := ownershipObject(t)
	holder := startOwnershipHelper(t, object, "hold", "held")

	contender := openOwnership(t, object, redundancy.RolePrimary)
	acquired, err := contender.TryAcquire()
	require.NoError(t, err)
	require.False(t, acquired.Held, "two processes held the ownership")

	holder.closeInput(t)
	holder.wait(t)

	acquired, err = contender.Acquire(t.Context())
	require.NoError(t, err)
	require.True(t, acquired.Held)
	require.False(t, acquired.Abandoned, "a planned handover must not be reported as a crash")
	require.NoError(t, contender.Release())
}

// TestOwnershipReleasesAcrossProcessesAfterForcedDeath proves both that a killed
// holder's ownership is retakeable with no cleanup logic, and that the taking
// process learns the previous owner died. The file lock this replaced released
// identically but could not report which had happened.
//
// The contender opens the ownership before the holder is killed, which is the real
// failover shape: a standby is already waiting on the object when the primary
// dies. It is also what makes abandonment observable at all, since a named object
// ceases to exist once its last handle closes.
func TestOwnershipReleasesAcrossProcessesAfterForcedDeath(t *testing.T) {
	object := ownershipObject(t)
	holder := startOwnershipHelper(t, object, "hold", "held")
	contender := openOwnership(t, object, redundancy.RolePrimary)

	holder.kill(t)

	acquired, err := contender.Acquire(t.Context())
	require.NoError(t, err)
	require.True(t, acquired.Held)
	require.True(t, acquired.Abandoned, "a forced death must be reported as abandonment")
	require.NoError(t, contender.Release())
}

// TestOwnershipReportsAbandonmentWhenAHolderExitsWithoutReleasing covers the exit
// path a crash actually takes: the process ends while still holding.
func TestOwnershipReportsAbandonmentWhenAHolderExitsWithoutReleasing(t *testing.T) {
	object := ownershipObject(t)
	holder := startOwnershipHelper(t, object, "abandon", "held")
	contender := openOwnership(t, object, redundancy.RolePrimary)

	holder.closeInput(t)
	holder.wait(t)

	acquired, err := contender.Acquire(t.Context())
	require.NoError(t, err)
	require.True(t, acquired.Abandoned)
}

// TestOwnershipAfterEveryProcessIsGoneReportsNoAbandonment records the boundary of
// the abandonment signal. A named object exists only while a handle to it is
// open, so a machine whose processes are all gone leaves nothing behind, and the
// next process to start creates a fresh object and takes it cleanly.
//
// This is correct rather than a gap: with nothing left holding the machine's
// resources there is nothing for the signal to warn about. It does mean
// abandonment reports a takeover from a dead peer, not a history of past crashes.
func TestOwnershipAfterEveryProcessIsGoneReportsNoAbandonment(t *testing.T) {
	object := ownershipObject(t)
	holder := startOwnershipHelper(t, object, "abandon", "held")
	holder.closeInput(t)
	holder.wait(t)

	contender := openOwnership(t, object, redundancy.RolePrimary)
	acquired, err := contender.Acquire(t.Context())
	require.NoError(t, err)
	require.True(t, acquired.Held)
	require.False(t, acquired.Abandoned)
}

func TestOwnershipWaitCancellationAcrossProcesses(t *testing.T) {
	object := ownershipObject(t)
	holder := startOwnershipHelper(t, object, "hold", "held")
	waiter := startOwnershipHelper(t, object, "wait", "waiting")

	waiter.closeInput(t)
	require.Equal(t, "cancelled", waiter.readLine(t))
	waiter.wait(t)

	contender := openOwnership(t, object, redundancy.RolePrimary)
	acquired, err := contender.TryAcquire()
	require.NoError(t, err)
	require.False(t, acquired.Held, "canceling the waiter disturbed the holder")

	holder.closeInput(t)
	holder.wait(t)
}

type ownershipHelper struct {
	command *exec.Cmd
	input   io.WriteCloser
	output  *bufio.Reader
	waited  bool
}

func startOwnershipHelper(t *testing.T, object, mode, ready string) *ownershipHelper {
	t.Helper()
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestOwnershipProcessHelper$")
	command.Env = append(os.Environ(), ownershipHelperMode+"="+mode, ownershipHelperObject+"="+object)
	input, err := command.StdinPipe()
	require.NoError(t, err)
	stdout, err := command.StdoutPipe()
	require.NoError(t, err)
	command.Stderr = os.Stderr
	require.NoError(t, command.Start())
	helper := &ownershipHelper{command: command, input: input, output: bufio.NewReader(stdout)}
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

func (h *ownershipHelper) readLine(t *testing.T) string {
	t.Helper()
	line, err := h.output.ReadString('\n')
	require.NoError(t, err)
	return strings.TrimSpace(line)
}

func (h *ownershipHelper) closeInput(t *testing.T) {
	t.Helper()
	require.NoError(t, h.input.Close())
}

func (h *ownershipHelper) wait(t *testing.T) {
	t.Helper()
	err := h.command.Wait()
	h.waited = true
	require.NoError(t, err)
}

func (h *ownershipHelper) kill(t *testing.T) {
	t.Helper()
	require.NoError(t, h.command.Process.Kill())
	err := h.command.Wait()
	h.waited = true
	require.Error(t, err)
}
