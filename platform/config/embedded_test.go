package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDeploymentDecodes guards against the checked-in mock descriptor drifting
// out of the deployment descriptor's JSON shape.
func TestDeploymentDecodes(t *testing.T) {
	d, err := Deployment()
	require.NoError(t, err)
	require.NotEmpty(t, d.Platform)
	require.NotEmpty(t, d.Machine)
	require.NotEmpty(t, d.Services)
}

func TestDecodeDescriptorRejectsMalformedJSON(t *testing.T) {
	_, err := decodeDescriptor([]byte(`{`))
	require.ErrorContains(t, err, "invalid deployment descriptor")
}
