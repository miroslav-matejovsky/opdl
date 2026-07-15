package deploymentdescriptors

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	platformdeployment "github.com/miroslav-matejovsky/opdl/platform/deployment"
)

// TestSignature is a small unit test of the structural JSON signature helper,
// exercised on a real descriptor sub-type. It pins the format the descriptor
// comparison relies on: JSON field names mapped to their kinds, sorted.
func TestSignature(t *testing.T) {
	require.Equal(t, "{chaos:bool,redundancy:bool}", signature(reflect.TypeFor[platformdeployment.Features]()))
}
