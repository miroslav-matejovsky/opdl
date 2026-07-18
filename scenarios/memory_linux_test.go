//go:build linux

package scenarios

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func processMemoryBytes(pid int) (uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, err
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "VmRSS:" && fields[2] == "kB" {
			kilobytes, parseErr := strconv.ParseUint(fields[1], 10, 64)
			return kilobytes * 1024, parseErr
		}
	}
	return 0, fmt.Errorf("process %d status has no VmRSS", pid)
}
