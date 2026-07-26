package deploymentdescriptors

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	platformconfig "github.com/miroslav-matejovsky/opdl/platform/config"
)

// TestSignature is a small unit test of the structural JSON signature helper,
// exercised on a real descriptor sub-type. It pins the format the descriptor
// comparison relies on: JSON field names mapped to their kinds, sorted.
func TestSignature(t *testing.T) {
	require.Equal(t, "{duration:string,failback_stabilization:string,file:string,health_check_interval:string,renewal_interval:string}", signature(reflect.TypeFor[platformconfig.Lease]()))
}
