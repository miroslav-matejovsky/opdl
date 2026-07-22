package winmutex_test

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/miroslav-matejovsky/opdl/utils/winmutex"
	"github.com/stretchr/testify/require"
)

const (
	holderModeEnv = "OPDL_WINMUTEX_HELPER_MODE"
	holderNameEnv = "OPDL_WINMUTEX_HELPER_NAME"
)

// TestWinmutexProcessHelper is re-executed as a child process by the tests below.
// Stdin is the control channel: EOF tells a holder to release and exit.
func TestWinmutexProcessHelper(t *testing.T) {
	if os.Getenv(holderModeEnv) == "" {
		return
	}
	m, err := winmutex.Open(os.Getenv(holderNameEnv))
	require.NoError(t, err)

	outcome, err := m.TryAcquire()
	require.NoError(t, err)
	require.True(t, outcome.Held(), "the helper could not take an uncontested mutex")

	// Announce ownership only once it is actually held, so the parent never races
	// the child's acquisition.
	if _, err := os.Stdout.WriteString("held\n"); err != nil {
		require.NoError(t, err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)

	if os.Getenv(holderModeEnv) == "graceful" {
		require.NoError(t, m.Release())
	}
}

// TestForcedDeathIsReportedAsAbandoned is the distinction the file lock this
// package replaces could not make: a waiter that takes over after a crash learns
// that the previous owner died rather than handed over.
func TestForcedDeathIsReportedAsAbandoned(t *testing.T) {
	t.Parallel()

	name := uniqueName(t)
	holder := startHolder(t, name, "hold")

	contender := open(t, name)
	outcome, err := contender.TryAcquire()
	require.NoError(t, err)
	require.Equal(t, winmutex.NotAcquired, outcome, "two processes held the mutex")

	holder.kill(t)

	outcome, err = contender.Acquire(t.Context())
	require.NoError(t, err)
	require.Equal(t, winmutex.AcquiredAbandoned, outcome)
	require.True(t, contender.Held())
}

// TestGracefulReleaseIsNotReportedAsAbandoned is the other half of the pair. If
// this passed while the forced-death test also passed only by luck, the
// distinction would be worthless.
func TestGracefulReleaseIsNotReportedAsAbandoned(t *testing.T) {
	t.Parallel()

	name := uniqueName(t)
	holder := startHolder(t, name, "graceful")

	contender := open(t, name)
	holder.closeInput(t)
	holder.wait(t)

	outcome, err := contender.Acquire(t.Context())
	require.NoError(t, err)
	require.Equal(t, winmutex.Acquired, outcome)
}

// TestOwnershipIsReleasedWhenAHolderExitsWithoutReleasing proves the kernel drops
// ownership at process exit even when the holder never calls Release, which is
// what makes crash recovery need no cleanup logic.
func TestOwnershipIsReleasedWhenAHolderExitsWithoutReleasing(t *testing.T) {
	t.Parallel()

	name := uniqueName(t)
	holder := startHolder(t, name, "hold")

	contender := open(t, name)
	holder.closeInput(t)
	holder.wait(t)

	outcome, err := contender.Acquire(t.Context())
	require.NoError(t, err)
	require.Equal(t, winmutex.AcquiredAbandoned, outcome, "an exit without Release abandons the mutex")
}

type holderProcess struct {
	command *exec.Cmd
	input   io.WriteCloser
	output  *bufio.Reader
	waited  bool
}

func startHolder(t *testing.T, name, mode string) *holderProcess {
	t.Helper()
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestWinmutexProcessHelper$")
	command.Env = append(os.Environ(), holderModeEnv+"="+mode, holderNameEnv+"="+name)
	input, err := command.StdinPipe()
	require.NoError(t, err)
	stdout, err := command.StdoutPipe()
	require.NoError(t, err)
	command.Stderr = os.Stderr
	require.NoError(t, command.Start())

	holder := &holderProcess{command: command, input: input, output: bufio.NewReader(stdout)}
	t.Cleanup(func() {
		if holder.waited {
			return
		}
		_ = holder.command.Process.Kill()
		_ = holder.command.Wait()
	})
	require.Equal(t, "held", holder.readLine(t))
	return holder
}

func (h *holderProcess) readLine(t *testing.T) string {
	t.Helper()
	line, err := h.output.ReadString('\n')
	require.NoError(t, err)
	return strings.TrimSpace(line)
}

func (h *holderProcess) closeInput(t *testing.T) {
	t.Helper()
	require.NoError(t, h.input.Close())
}

func (h *holderProcess) wait(t *testing.T) {
	t.Helper()
	err := h.command.Wait()
	h.waited = true
	require.NoError(t, err)
}

func (h *holderProcess) kill(t *testing.T) {
	t.Helper()
	require.NoError(t, h.command.Process.Kill())
	err := h.command.Wait()
	h.waited = true
	require.Error(t, err)
}
