package procrun

import (
	"os/exec"
	"sync"

	"github.com/miroslav-matejovsky/opdl/utils/processtree"
)

// Process is a child command under test control: started inside a kill-on-close
// process tree, observable while it runs, and collectable once it exits.
type Process struct {
	cmd    *exec.Cmd
	tree   *processtree.Owner
	output *syncBuf
	done   chan struct{}
	errMu  sync.Mutex
	err    error
}

// Start runs cmd inside a kill-on-close process tree, capturing stdout and
// stderr into one buffer.
func Start(cmd *exec.Cmd) (*Process, error) {
	out := &syncBuf{}
	cmd.Stdout = out
	cmd.Stderr = out

	tree, err := processtree.Start(cmd)
	if err != nil {
		return nil, err
	}

	p := &Process{
		cmd:    cmd,
		tree:   tree,
		output: out,
		done:   make(chan struct{}),
	}
	go func() {
		err := cmd.Wait()
		p.errMu.Lock()
		p.err = err
		p.errMu.Unlock()
		close(p.done)
	}()

	return p, nil
}

// PID returns the operating system process ID of the started child command.
func (p *Process) PID() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Running reports whether the child process is currently running without blocking.
func (p *Process) Running() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// Logs returns a snapshot of the process output captured so far. It is safe to
// call while the process is still running and does not terminate the process.
func (p *Process) Logs() string {
	return p.output.String()
}

// Stop sends the graceful termination signal to the process and returns immediately.
// The caller decides how long to wait before falling back to Kill.
func (p *Process) Stop() error {
	return p.tree.Stop()
}

// Kill force-terminates the process and its process tree, blocking until exited
// and returning the final exit error.
func (p *Process) Kill() error {
	_ = p.tree.Kill()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	<-p.done
	p.errMu.Lock()
	err := p.err
	p.errMu.Unlock()
	return err
}

// Wait blocks until the process finishes and returns the complete output and
// exit error. Because it waits for the exec output copying goroutines to finish,
// the returned output is complete.
func (p *Process) Wait() (string, error) {
	<-p.done
	_ = p.tree.Close()
	p.errMu.Lock()
	err := p.err
	p.errMu.Unlock()
	return p.output.String(), err
}
