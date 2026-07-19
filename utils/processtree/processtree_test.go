package processtree_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"testing"

	"github.com/miroslav-matejovsky/opdl/utils/processtree"
	"github.com/stretchr/testify/require"
)

const helperEnv = "PROCESSTREE_HELPER_MODE"

func TestHelperProcessMain(t *testing.T) {
	mode := os.Getenv(helperEnv)
	if mode == "" {
		return
	}
	switch mode {
	case "parent":
		// Start a child/grandchild and report PID.
		cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=TestHelperProcessMain")
		cmd.Env = append(os.Environ(), helperEnv+"=child")
		if err := cmd.Start(); err != nil {
			os.Exit(1)
		}
		fmt.Println(cmd.Process.Pid)
		// Block until terminated.
		<-make(chan struct{})
	case "child":
		<-make(chan struct{})
	case "graceful":
		// Block until graceful stop or kill.
		<-make(chan struct{})
	case "exit":
		return
	}
	os.Exit(0)
}

func TestStartAndKillDescendants(t *testing.T) {
	if os.Getenv(helperEnv) != "" {
		return
	}
	t.Parallel()

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=TestHelperProcessMain")
	cmd.Env = append(os.Environ(), helperEnv+"=parent")

	owner, err := processtree.Start(cmd)
	require.NoError(t, err)

	require.NoError(t, owner.Kill())
	_ = cmd.Wait()
	require.NoError(t, owner.Close())

	// Repeated calls are safe.
	require.NoError(t, owner.Kill())
	require.NoError(t, owner.Close())
}

func TestContextCancellationKillsTree(t *testing.T) {
	if os.Getenv(helperEnv) != "" {
		return
	}
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestHelperProcessMain")
	cmd.Env = append(os.Environ(), helperEnv+"=parent")

	owner, err := processtree.Start(cmd)
	require.NoError(t, err)

	cancel()

	err = cmd.Wait()
	require.Error(t, err)
	require.NoError(t, owner.Close())
}

func TestKillAfterProcessExitIsIdempotent(t *testing.T) {
	if os.Getenv(helperEnv) != "" {
		return
	}
	t.Parallel()

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=TestHelperProcessMain")
	cmd.Env = append(os.Environ(), helperEnv+"=exit")

	owner, err := processtree.Start(cmd)
	require.NoError(t, err)
	require.NoError(t, cmd.Wait())
	require.NoError(t, owner.Kill())
	require.NoError(t, owner.Close())
}

func TestConcurrentSafetyAndIdempotency(t *testing.T) {
	if os.Getenv(helperEnv) != "" {
		return
	}
	t.Parallel()

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=TestHelperProcessMain")
	cmd.Env = append(os.Environ(), helperEnv+"=child")

	owner, err := processtree.Start(cmd)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			_ = owner.Stop()
		}()
		go func() {
			defer wg.Done()
			_ = owner.Kill()
		}()
		go func() {
			defer wg.Done()
			_ = owner.Close()
		}()
	}
	wg.Wait()
	_ = cmd.Wait()
}
