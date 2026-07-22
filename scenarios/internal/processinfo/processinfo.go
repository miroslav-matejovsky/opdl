package processinfo

import (
	"context"
	"fmt"
)

// ResidentBytes reports the resident memory (working set or RSS) of process pid
// in bytes.
//
// ResidentBytes propagates ctx cancellation and returns an error if pid is non-positive
// or if the operating system fails to query the process.
func ResidentBytes(ctx context.Context, pid int) (uint64, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("processinfo: invalid pid %d", pid)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return residentBytes(ctx, pid)
}
