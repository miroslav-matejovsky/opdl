//go:build windows

package processinfo

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ParseWindowsWorkingSet parses the string output of PowerShell Get-Process WorkingSet64.
func ParseWindowsWorkingSet(output string, pid int) (uint64, error) {
	bytes, err := strconv.ParseUint(strings.TrimSpace(output), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("processinfo: parse process %d working set: %w", pid, err)
	}
	return bytes, nil
}

func residentBytes(ctx context.Context, pid int) (uint64, error) {
	command := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command",
		fmt.Sprintf("(Get-Process -Id %d).WorkingSet64", pid))
	output, err := command.Output()
	if err != nil {
		return 0, fmt.Errorf("processinfo: read process %d working set: %w", pid, err)
	}
	return ParseWindowsWorkingSet(string(output), pid)
}
