//go:build windows

package processinfo_test

import (
	"testing"

	"github.com/miroslav-matejovsky/opdl/utils/processinfo"
	"github.com/stretchr/testify/require"
)

func TestParseWindowsWorkingSet(t *testing.T) {
	t.Parallel()

	bytes, err := processinfo.ParseWindowsWorkingSet(" 2097152 \r\n", 1234)
	require.NoError(t, err)
	require.Equal(t, uint64(2097152), bytes)

	_, err = processinfo.ParseWindowsWorkingSet("invalid", 1234)
	require.Error(t, err)
}
