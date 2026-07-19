//go:build linux

package processinfo

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ParseLinuxStatus parses the contents of a /proc/<pid>/status file and extracts
// the VmRSS value converted from kilobytes to bytes.
func ParseLinuxStatus(data string, pid int) (uint64, error) {
	for line := range strings.SplitSeq(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "VmRSS:" && fields[2] == "kB" {
			kilobytes, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("processinfo: parse process %d VmRSS: %w", pid, err)
			}
			return kilobytes * 1024, nil
		}
	}
	return 0, fmt.Errorf("processinfo: process %d status has no VmRSS", pid)
}

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
