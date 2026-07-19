//go:build linux

package processinfo

import (
	"context"
	"fmt"
	"os"
)

func residentBytes(ctx context.Context, pid int) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	path := fmt.Sprintf("/proc/%d/status", pid)
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("processinfo: read process %d status: %w", pid, err)
	}
	return ParseLinuxStatus(string(data), pid)
}
