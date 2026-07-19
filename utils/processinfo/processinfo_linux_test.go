//go:build linux

package processinfo_test

import (
	"testing"

	"github.com/miroslav-matejovsky/opdl/utils/processinfo"
	"github.com/stretchr/testify/require"
)

func TestParseLinuxStatus(t *testing.T) {
	t.Parallel()

	valid := "Name:\ttest\nState:\tR (running)\nVmRSS:\t   1024 kB\nVmSize:\t   4096 kB\n"
	bytes, err := processinfo.ParseLinuxStatus(valid, 1234)
	require.NoError(t, err)
	require.Equal(t, uint64(1024*1024), bytes)

	missing := "Name:\ttest\nState:\tR (running)\nVmSize:\t   4096 kB\n"
	_, err = processinfo.ParseLinuxStatus(missing, 1234)
	require.Error(t, err)

	malformed := "Name:\ttest\nVmRSS:\t   not-a-number kB\n"
	_, err = processinfo.ParseLinuxStatus(malformed, 1234)
	require.Error(t, err)
}
