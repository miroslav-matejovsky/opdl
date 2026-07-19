//go:build !windows

package processtree

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
)

// Owner manages a running child process and its descendants inside an OS container.
type Owner struct {
	process *os.Process
	pgid    int
	once    sync.Once
	err     error
}

// ConfigureGraceful prepares cmd to run in a distinct process group.
func ConfigureGraceful(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// Start launches cmd in a distinct process group and returns an Owner to manage its tree.
func Start(cmd *exec.Cmd) (*Owner, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true

	owner := &Owner{}
	cmd.Cancel = owner.Kill

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	owner.process = cmd.Process
	if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
		owner.pgid = pgid
	} else {
		owner.pgid = cmd.Process.Pid
	}
	return owner, nil
}

// Close releases any resources associated with the owner. It is idempotent.
func (o *Owner) Close() error {
	return nil
}

// Kill forcefully terminates the process group (and direct child fallback).
func (o *Owner) Kill() error {
	o.once.Do(func() {
		if o.pgid > 0 {
			o.err = syscall.Kill(-o.pgid, syscall.SIGKILL)
			if o.err != nil && o.process != nil {
				_ = o.process.Kill()
			}
		} else if o.process != nil {
			o.err = o.process.Kill()
		}
	})
	return o.err
}

// Stop sends SIGTERM to the process group (and direct child fallback).
func (o *Owner) Stop() error {
	if o.pgid > 0 {
		if err := syscall.Kill(-o.pgid, syscall.SIGTERM); err == nil {
			return nil
		}
	}
	if o.process != nil {
		return o.process.Signal(syscall.SIGTERM)
	}
	return nil
}
