//go:build windows

package scenarios

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"os/exec"
)

func processMemoryBytes(pid int) (uint64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command",
		fmt.Sprintf("(Get-Process -Id %d).WorkingSet64", pid))
	output, err := command.Output()
	if err != nil {
		return 0, fmt.Errorf("read process %d working set: %w", pid, err)
	}
	bytes, err := strconv.ParseUint(strings.TrimSpace(string(output)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse process %d working set: %w", pid, err)
	}
	return bytes, nil
}
