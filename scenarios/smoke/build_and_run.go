package smoke

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/scenarios/internal/harness"
)

// BuildAndRunSingleMachine builds the simplest deployment there is and asks the
// running platform who it is.
//
// One machine, one Primary Instance, no standby: a site with nothing to
// coordinate with and nothing to fail over to. Its journal is a single replica
// on the one instance that runs, so the whole scenario is one process from build
// to answer. Nothing here imports platform code; the binary under test is the
// one the builder produced a moment earlier.
func BuildAndRunSingleMachine(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	outDir := filepath.Join(harness.ScenarioDir(t), "out")
	deployment := harness.DeploySite(ctx, t, outDir, filepath.Join(harness.ScenarioDir(t), "work"), "simple")
	node := deployment.Machine(t, "node-a")

	// The package is the first observable result: a machine that deploys no
	// standby must be packaged with only the primary launch, because a launch
	// record in the manifest is what a service installer would act on.
	manifest := harness.ReadManifest(t, node.BinaryPath)
	require.Nil(t, manifest.Standby, "a machine with no standby packages only the primary launch")
	require.Equal(t, []string{"-instance", "primary"}, manifest.Primary.Args)

	// StartSite waits for the instance to report itself active, which is the
	// runtime's own statement that it bound its API, brought its Event Fabric up,
	// and replayed the journal.
	deployment.StartSite(ctx, t)

	instance, code := harness.GetInstance(ctx, t, node)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "node-a", instance.Machine, "the instance reports the descriptor machine it was built for")
	require.Equal(t, harness.RolePrimary, instance.Role, "the role is fixed at build time")
	require.Equal(t, harness.InstanceStateActive, instance.State,
		"the only instance of the machine holds Primary Ownership, so it serves")
	require.Equal(t, node.Sockets.API, instance.Address,
		"the instance answers at the address the builder resolved from its authored local_port")
	require.Empty(t, instance.PeerAddress, "a machine with one instance has no peer to name")

	// The machine reported the configuration it booted with. The peers line is
	// the site's membership, and this site's membership is one instance.
	logs := node.Output()
	require.Contains(t, logs, "platform configuration")
	require.Contains(t, logs, "peers        node-a/primary (127.0.0.1)",
		"a single-machine site's membership is its one Primary Instance")
	require.Contains(t, logs, "data_dir     "+filepath.ToSlash(node.Sockets.DataDir))
	require.Contains(t, logs, "credentials_file=(none: loopback only)",
		"a loopback deployment may run unauthenticated, and says so")
}
