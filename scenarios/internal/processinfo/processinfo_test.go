package processinfo_test

import (
	"context"
	"os"
	"testing"

	"github.com/miroslav-matejovsky/opdl/scenarios/internal/processinfo"
	"github.com/stretchr/testify/require"
)

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
