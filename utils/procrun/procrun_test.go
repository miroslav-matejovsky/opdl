package procrun

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	if os.Getenv("PROCRUN_TEST_HELPER") == "1" {
		mode := os.Getenv("PROCRUN_TEST_MODE")
		switch mode {
		case "output":
			fmt.Print("hello stdout\n")
			_, _ = fmt.Fprint(os.Stderr, "hello stderr\n")
			os.Exit(0)
		case "exitcode":
			os.Exit(42)
		case "sleep":
			time.Sleep(10 * time.Second)
			os.Exit(0)
		case "continuous":
			for i := 0; i < 50; i++ {
				fmt.Printf("line %d\n", i)
				time.Sleep(5 * time.Millisecond)
			}
			os.Exit(0)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func helperCmd(ctx context.Context, mode string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0])
	cmd.Env = append(os.Environ(), "PROCRUN_TEST_HELPER=1", "PROCRUN_TEST_MODE="+mode)
	return cmd
}

func TestProcessOutputAndExitCode(t *testing.T) {
	ctx := t.Context()
	p, err := Start(helperCmd(ctx, "output"))
	require.NoError(t, err)
	require.NotZero(t, p.PID())

	out, err := p.Wait()
	require.NoError(t, err)
	require.False(t, p.Running())
	require.Contains(t, out, "hello stdout\n")
	require.Contains(t, out, "hello stderr\n")
	require.Equal(t, out, p.Logs())

	// Calling Wait twice is safe and returns the same output/error.
	out2, err2 := p.Wait()
	require.NoError(t, err2)
	require.Equal(t, out, out2)
}

func TestProcessExitCodePropagation(t *testing.T) {
	ctx := t.Context()
	p, err := Start(helperCmd(ctx, "exitcode"))
	require.NoError(t, err)

	_, err = p.Wait()
	require.Error(t, err)
	require.Contains(t, err.Error(), "exit status 42")
}

func TestProcessKillAndRunning(t *testing.T) {
	ctx := t.Context()
	p, err := Start(helperCmd(ctx, "sleep"))
	require.NoError(t, err)
	require.True(t, p.Running())

	// Kill blocks until the child is gone and reports the exit result. The result
	// itself is deliberately not asserted to be an error: on Unix the child dies
	// from a signal, but on Windows the tree is torn down by closing the job
	// handle with KILL_ON_JOB_CLOSE, which exits the child with code 0. What
	// callers rely on is that the process is dead once Kill returns.
	killErr := p.Kill()
	require.False(t, p.Running())

	// Kill on an already exited process does not panic or block, and it agrees
	// with the first call and with Wait about how the process ended.
	require.Equal(t, killErr, p.Kill())
	_, waitErr := p.Wait()
	require.Equal(t, killErr, waitErr)
}

func TestProcessLogsConcurrentWithWriter(t *testing.T) {
	ctx := t.Context()
	p, err := Start(helperCmd(ctx, "continuous"))
	require.NoError(t, err)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for p.Running() {
			_ = p.Logs()
			time.Sleep(2 * time.Millisecond)
		}
	}()
	go func() {
		defer wg.Done()
		_, _ = p.Wait()
	}()
	wg.Wait()
	require.Contains(t, p.Logs(), "line ")
}
