//go:build windows

package processtree

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

const (
	createNewProcessGroup = 0x00000200
	createSuspended       = 0x00000004
	ctrlBreakEvent        = 1

	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x00002000
	threadSnapshot                    = 0x00000004
	threadSuspendResume               = 0x0002
	failedThreadResume                = 0xffffffff
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	assignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	closeHandle              = kernel32.NewProc("CloseHandle")
	createJobObject          = kernel32.NewProc("CreateJobObjectW")
	createToolhelp32Snapshot = kernel32.NewProc("CreateToolhelp32Snapshot")
	generateConsoleCtrlEvent = kernel32.NewProc("GenerateConsoleCtrlEvent")
	openThread               = kernel32.NewProc("OpenThread")
	resumeThread             = kernel32.NewProc("ResumeThread")
	setInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	thread32First            = kernel32.NewProc("Thread32First")
	thread32Next             = kernel32.NewProc("Thread32Next")
)

type jobObjectBasicLimitInformation struct {
	perProcessUserTimeLimit int64
	perJobUserTimeLimit     int64
	limitFlags              uint32
	minimumWorkingSetSize   uintptr
	maximumWorkingSetSize   uintptr
	activeProcessLimit      uint32
	affinity                uintptr
	priorityClass           uint32
	schedulingClass         uint32
}

type ioCounters struct {
	readOperationCount  uint64
	writeOperationCount uint64
	otherOperationCount uint64
	readTransferCount   uint64
	writeTransferCount  uint64
	otherTransferCount  uint64
}

type jobObjectExtendedLimitInfo struct {
	basicLimitInformation jobObjectBasicLimitInformation
	ioInfo                ioCounters
	processMemoryLimit    uintptr
	jobMemoryLimit        uintptr
	peakProcessMemoryUsed uintptr
	peakJobMemoryUsed     uintptr
}

type threadEntry struct {
	size           uint32
	usage          uint32
	threadID       uint32
	ownerProcessID uint32
	basePriority   int32
	deltaPriority  int32
	flags          uint32
}

// Owner manages a running child process and its descendants inside an OS container.
type Owner struct {
	handle  syscall.Handle
	process *os.Process
	ready   chan struct{}
	once    sync.Once
	err     error
}

// ConfigureGraceful configures cmd so that graceful console control signals
// can be sent without targeting the parent process group.
func ConfigureGraceful(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNewProcessGroup
}

// Start launches cmd inside an OS process container (job object on Windows) and
// returns an Owner to manage its lifecycle and containment.
func Start(command *exec.Cmd) (*Owner, error) {
	owner, err := newOwner()
	if err != nil {
		return nil, err
	}
	defer close(owner.ready)
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.CreationFlags |= createSuspended
	command.Cancel = owner.Kill
	if err := command.Start(); err != nil {
		_ = owner.Close()
		return nil, err
	}
	owner.process = command.Process
	if err := owner.assign(command.Process); err != nil {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
		_ = owner.Close()
		return nil, err
	}
	if err := resumeProcess(command.Process.Pid); err != nil {
		_ = owner.Kill()
		_, _ = command.Process.Wait()
		return nil, err
	}
	return owner, nil
}

func newOwner() (*Owner, error) {
	handle, _, callErr := createJobObject.Call(0, 0)
	if handle == 0 {
		return nil, windowsError("create process job", callErr)
	}
	owner := &Owner{handle: syscall.Handle(handle), ready: make(chan struct{})}
	info := jobObjectExtendedLimitInfo{}
	info.basicLimitInformation.limitFlags = jobObjectLimitKillOnJobClose
	ok, _, callErr := setInformationJobObject.Call(
		handle,
		jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	if ok == 0 {
		_ = owner.Close()
		return nil, windowsError("configure process job", callErr)
	}
	return owner, nil
}

func (o *Owner) assign(process *os.Process) error {
	var assignErr error
	if err := process.WithHandle(func(processHandle uintptr) {
		ok, _, callErr := assignProcessToJobObject.Call(uintptr(o.handle), processHandle)
		if ok == 0 {
			assignErr = windowsError("assign process to job", callErr)
		}
	}); err != nil {
		return fmt.Errorf("access process handle: %w", err)
	}
	return assignErr
}

// Close closes the OS handle for the process container. Close is idempotent and concurrency safe.
func (o *Owner) Close() error {
	o.once.Do(func() {
		ok, _, callErr := closeHandle.Call(uintptr(o.handle))
		if ok == 0 {
			o.err = windowsError("close process job", callErr)
		}
	})
	return o.err
}

// Kill terminates the process container and all its descendants, and closes handles.
func (o *Owner) Kill() error {
	if o.ready != nil {
		<-o.ready
	}
	return o.Close()
}

// Stop sends a graceful termination signal (CTRL_BREAK_EVENT) to the child process group.
func (o *Owner) Stop() error {
	if o.process == nil {
		return nil
	}
	ok, _, callErr := generateConsoleCtrlEvent.Call(ctrlBreakEvent, uintptr(o.process.Pid))
	if ok == 0 {
		return fmt.Errorf("signal process group %d: %w", o.process.Pid, callErr)
	}
	return nil
}

func resumeProcess(pid int) error {
	snapshot, _, callErr := createToolhelp32Snapshot.Call(threadSnapshot, 0)
	if snapshot == uintptr(syscall.InvalidHandle) {
		return windowsError("snapshot process threads", callErr)
	}
	defer closeHandle.Call(snapshot) //nolint:errcheck // Best-effort handle release.

	entry := threadEntry{size: uint32(unsafe.Sizeof(threadEntry{}))}
	ok, _, callErr := thread32First.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	if ok == 0 {
		return windowsError("read first process thread", callErr)
	}
	for {
		if entry.ownerProcessID == uint32(pid) {
			thread, _, openErr := openThread.Call(threadSuspendResume, 0, uintptr(entry.threadID))
			if thread == 0 {
				return windowsError("open suspended process thread", openErr)
			}
			resumed, _, resumeErr := resumeThread.Call(thread)
			_, _, _ = closeHandle.Call(thread)
			if resumed == uintptr(failedThreadResume) {
				return windowsError("resume process thread", resumeErr)
			}
			return nil
		}
		ok, _, callErr = thread32Next.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
		if ok == 0 {
			return windowsError(fmt.Sprintf("find suspended thread for process %d", pid), callErr)
		}
	}
}

func windowsError(action string, err error) error {
	if errors.Is(err, syscall.Errno(0)) {
		err = syscall.EINVAL
	}
	return fmt.Errorf("%s: %w", action, err)
}
