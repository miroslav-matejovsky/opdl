//go:build !linux && !windows

package processinfo_test

import (
	"testing"

	"github.com/miroslav-matejovsky/opdl/utils/processinfo"
	"github.com/stretchr/testify/require"
)

func TestParsePSRSS(t *testing.T) {
	t.Parallel()

	bytes, err := processinfo.ParsePSRSS(" 512 \n", 1234)
	require.NoError(t, err)
	require.Equal(t, uint64(512*1024), bytes)

	_, err = processinfo.ParsePSRSS("invalid", 1234)
	require.Error(t, err)
}
