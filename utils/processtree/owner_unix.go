//go:build !windows

package processtree

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

// Owner manages a running child process and its descendants inside an OS container.
type Owner struct {
	process *os.Process
	pgid    int
	ready   chan struct{}
	once    sync.Once
	err     error
}

// Start launches cmd in a distinct process group and returns an Owner to manage its tree.
func Start(cmd *exec.Cmd) (*Owner, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true

	owner := &Owner{ready: make(chan struct{})}
	defer close(owner.ready)
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
	if o.ready != nil {
		<-o.ready
	}
	o.once.Do(func() {
		if o.pgid > 0 {
			o.err = syscall.Kill(-o.pgid, syscall.SIGKILL)
			if errors.Is(o.err, syscall.ESRCH) {
				o.err = nil
			} else if o.err != nil && o.process != nil {
				_ = o.process.Kill()
			}
		} else if o.process != nil {
			o.err = o.process.Kill()
			if errors.Is(o.err, os.ErrProcessDone) {
				o.err = nil
			}
		}
	})
	return o.err
}

// Stop sends SIGTERM to the process group (and direct child fallback).
func (o *Owner) Stop() error {
	if o.pgid > 0 {
		if err := syscall.Kill(-o.pgid, syscall.SIGTERM); err == nil {
			return nil
		} else if errors.Is(err, syscall.ESRCH) {
			return nil
		}
	}
	if o.process != nil {
		err := o.process.Signal(syscall.SIGTERM)
		if errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		return err
	}
	return nil
}
