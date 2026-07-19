//go:build windows

package scenarios

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"
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
	waitObject0                       = 0x00000000
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
	waitForSingleObject      = kernel32.NewProc("WaitForSingleObject")
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

// processTree owns a Windows job configured to terminate every member when its
// handle closes. The OS closes the handle even when the test process exits on a
// hard timeout, so descendants cannot outlive the scenario harness.
type processTree struct {
	handle syscall.Handle
	once   sync.Once
	err    error
}

func startCommand(command *exec.Cmd) (*processTree, error) {
	tree, err := newProcessTree()
	if err != nil {
		return nil, err
	}
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.CreationFlags |= createSuspended
	if command.Cancel != nil {
		command.Cancel = tree.kill
	}
	if err := command.Start(); err != nil {
		_ = tree.close()
		return nil, err
	}
	if err := tree.assign(command.Process); err != nil {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
		_ = tree.close()
		return nil, err
	}
	if err := resumeProcess(command.Process.Pid); err != nil {
		_ = tree.kill()
		_, _ = command.Process.Wait()
		return nil, err
	}
	return tree, nil
}

func newProcessTree() (*processTree, error) {
	handle, _, callErr := createJobObject.Call(0, 0)
	if handle == 0 {
		return nil, windowsError("create process job", callErr)
	}
	tree := &processTree{handle: syscall.Handle(handle)}
	info := jobObjectExtendedLimitInfo{}
	info.basicLimitInformation.limitFlags = jobObjectLimitKillOnJobClose
	ok, _, callErr := setInformationJobObject.Call(
		handle,
		jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	if ok == 0 {
		_ = tree.close()
		return nil, windowsError("configure process job", callErr)
	}
	return tree, nil
}

func (p *processTree) assign(process *os.Process) error {
	var assignErr error
	if err := process.WithHandle(func(processHandle uintptr) {
		ok, _, callErr := assignProcessToJobObject.Call(uintptr(p.handle), processHandle)
		if ok == 0 {
			assignErr = windowsError("assign process to job", callErr)
		}
	}); err != nil {
		return fmt.Errorf("access process handle: %w", err)
	}
	return assignErr
}

func (p *processTree) close() error {
	p.once.Do(func() {
		ok, _, callErr := closeHandle.Call(uintptr(p.handle))
		if ok == 0 {
			p.err = windowsError("close process job", callErr)
		}
	})
	return p.err
}

func (p *processTree) kill() error {
	return p.close()
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

func configureManagedCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

func signalManagedProcess(process *os.Process) error {
	ok, _, callErr := generateConsoleCtrlEvent.Call(ctrlBreakEvent, uintptr(process.Pid))
	if ok == 0 {
		return fmt.Errorf("signal process group %d: %w", process.Pid, callErr)
	}
	return nil
}

const processTreeHelper = "OPDL_WINDOWS_PROCESS_TREE_HELPER"

func TestWindowsProcessTreeKillsDescendants(t *testing.T) {
	switch os.Getenv(processTreeHelper) {
	case "parent":
		command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestWindowsProcessTreeKillsDescendants$")
		command.Env = append(os.Environ(), processTreeHelper+"=child")
		require.NoError(t, command.Start())
		fmt.Println(command.Process.Pid)
		<-make(chan struct{})
	case "child":
		<-make(chan struct{})
	}

	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestWindowsProcessTreeKillsDescendants$")
	command.Env = append(os.Environ(), processTreeHelper+"=parent")
	stdout, err := command.StdoutPipe()
	require.NoError(t, err)
	tree, err := startCommand(command)
	require.NoError(t, err)

	scanner := bufio.NewScanner(stdout)
	require.True(t, scanner.Scan(), "helper did not report its child PID")
	childPID, err := strconv.Atoi(scanner.Text())
	require.NoError(t, err)
	require.NoError(t, tree.kill())
	_ = command.Wait()

	require.Eventually(t, func() bool { return processExited(childPID) },
		5*time.Second, 10*time.Millisecond, "descendant process %d survived its job", childPID)
}

func processExited(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return true
	}
	exited := false
	err = process.WithHandle(func(handle uintptr) {
		result, _, _ := waitForSingleObject.Call(handle, 0)
		exited = result == waitObject0
	})
	_ = process.Release()
	return err != nil || exited
}
