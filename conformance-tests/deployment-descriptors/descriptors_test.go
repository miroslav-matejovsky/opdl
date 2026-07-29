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

// TestHealthContractSignatures pins the shape of the two halves of the health
// contract. The local half carries the probe endpoint; the remote half carries
// identity, expected observers, and freshness, and no way to reach anything.
//
// checkContractsMatch already fails when the builder and the platform diverge,
// but it would keep passing if both were changed together. These assertions are
// what make a change to the shape deliberate.
func TestHealthContractSignatures(t *testing.T) {
	require.Equal(t,
		"{health_check:{interval:string,path:string,port:int,retries:int,timeout:string,type:string},name:string,role:string}",
		signature(reflect.TypeFor[platformconfig.Service]()),
		"a hosted service carries the policy the platform probes it with")
	require.Equal(t,
		"{fresh_for:string,machine:string,machine_profile:string,observer_roles:[]string,service:string,service_role:string}",
		signature(reflect.TypeFor[platformconfig.SiteService]()),
		"a site unit carries no endpoint: only the machine hosting a service probes it")
}
