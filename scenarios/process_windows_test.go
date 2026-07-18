//go:build windows

package scenarios

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

const (
	createNewProcessGroup = 0x00000200
	ctrlBreakEvent        = 1
)

var generateConsoleCtrlEvent = syscall.NewLazyDLL("kernel32.dll").NewProc("GenerateConsoleCtrlEvent")

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
