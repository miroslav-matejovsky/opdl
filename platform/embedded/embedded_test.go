package embedded_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/embedded"
)

// TestDeploymentDecodes guards against the checked-in mock descriptor drifting
// out of the deployment descriptor's JSON shape.
func TestDeploymentDecodes(t *testing.T) {
	d, err := embedded.Deployment()
	require.NoError(t, err)
	require.NotEmpty(t, d.Platform)
	require.NotEmpty(t, d.Machine)
	require.NotEmpty(t, d.Services)
}
