//go:build !windows

package scenarios

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
)

type processTree struct {
	process *os.Process
	once    sync.Once
}

func startCommand(command *exec.Cmd) (*processTree, error) {
	if err := command.Start(); err != nil {
		return nil, err
	}
	return &processTree{process: command.Process}, nil
}

func (*processTree) close() error { return nil }

func (p *processTree) kill() error {
	var err error
	p.once.Do(func() { err = p.process.Kill() })
	return err
}

func configureManagedCommand(*exec.Cmd) {}

func signalManagedProcess(process *os.Process) error {
	return process.Signal(syscall.SIGTERM)
}
