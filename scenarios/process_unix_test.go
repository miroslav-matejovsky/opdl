//go:build !windows

package scenarios

import (
	"os"
	"os/exec"
	"syscall"
)

func configureManagedCommand(*exec.Cmd) {}

func signalManagedProcess(process *os.Process) error {
	return process.Signal(syscall.SIGTERM)
}
