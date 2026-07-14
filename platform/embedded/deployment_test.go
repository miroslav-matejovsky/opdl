package embedded

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodeDeploymentRejectsMalformedJSON(t *testing.T) {
	_, err := decodeDeployment([]byte(`{`))
	require.ErrorContains(t, err, "invalid deployment descriptor")
}
