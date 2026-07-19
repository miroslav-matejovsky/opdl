//go:build !linux && !windows

package processinfo

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
)

func residentBytes(ctx context.Context, pid int) (uint64, error) {
	command := exec.CommandContext(ctx, "ps", "-o", "rss=", "-p", strconv.Itoa(pid))
	output, err := command.Output()
	if err != nil {
		return 0, fmt.Errorf("processinfo: read process %d resident memory: %w", pid, err)
	}
	return ParsePSRSS(string(output), pid)
}
