//go:build !linux && !windows

package processinfo

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ParsePSRSS parses the string output of ps -o rss= -p <pid> converted from kilobytes to bytes.
func ParsePSRSS(output string, pid int) (uint64, error) {
	kilobytes, err := strconv.ParseUint(strings.TrimSpace(output), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("processinfo: parse process %d resident memory: %w", pid, err)
	}
	return kilobytes * 1024, nil
}

func residentBytes(ctx context.Context, pid int) (uint64, error) {
	command := exec.CommandContext(ctx, "ps", "-o", "rss=", "-p", strconv.Itoa(pid))
	output, err := command.Output()
	if err != nil {
		return 0, fmt.Errorf("processinfo: read process %d resident memory: %w", pid, err)
	}
	return ParsePSRSS(string(output), pid)
}
