package processinfo_test

import (
	"context"
	"os"
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

func TestParseWindowsWorkingSet(t *testing.T) {
	t.Parallel()

	bytes, err := processinfo.ParseWindowsWorkingSet(" 2097152 \r\n", 1234)
	require.NoError(t, err)
	require.Equal(t, uint64(2097152), bytes)

	_, err = processinfo.ParseWindowsWorkingSet("invalid", 1234)
	require.Error(t, err)
}

func TestParsePSRSS(t *testing.T) {
	t.Parallel()

	bytes, err := processinfo.ParsePSRSS(" 512 \n", 1234)
	require.NoError(t, err)
	require.Equal(t, uint64(512*1024), bytes)

	_, err = processinfo.ParsePSRSS("invalid", 1234)
	require.Error(t, err)
}

func TestResidentBytes_InvalidPIDs(t *testing.T) {
	t.Parallel()

	for _, pid := range []int{0, -1, -100} {
		_, err := processinfo.ResidentBytes(t.Context(), pid)
		require.Error(t, err)
	}
}

func TestResidentBytes_CurrentProcess(t *testing.T) {
	t.Parallel()

	bytes, err := processinfo.ResidentBytes(t.Context(), os.Getpid())
	require.NoError(t, err)
	require.Greater(t, bytes, uint64(0))
}

func TestResidentBytes_ContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := processinfo.ResidentBytes(ctx, os.Getpid())
	require.Error(t, err)
}
