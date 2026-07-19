package processinfo

import (
	"fmt"
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

// ParseWindowsWorkingSet parses the string output of PowerShell Get-Process WorkingSet64.
func ParseWindowsWorkingSet(output string, pid int) (uint64, error) {
	bytes, err := strconv.ParseUint(strings.TrimSpace(output), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("processinfo: parse process %d working set: %w", pid, err)
	}
	return bytes, nil
}

// ParsePSRSS parses the string output of ps -o rss= -p <pid> converted from kilobytes to bytes.
func ParsePSRSS(output string, pid int) (uint64, error) {
	kilobytes, err := strconv.ParseUint(strings.TrimSpace(output), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("processinfo: parse process %d resident memory: %w", pid, err)
	}
	return kilobytes * 1024, nil
}
