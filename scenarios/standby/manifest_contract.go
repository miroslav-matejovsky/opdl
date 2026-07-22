package standby

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/scenarios/internal/harness"
)

// ManifestArgumentsMatchRuntime consumes the packaged launch contract
// without importing builder or platform internals.
func ManifestArgumentsMatchRuntime(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	outDir := filepath.Join(harness.ScenarioDir(t), "out")
	deployment := harness.DeploySite(ctx, t, outDir, filepath.Join(harness.ScenarioDir(t), "work"), "manifest-contract")
	node := deployment.Machine(t, "node-b")
	// The subtests start node-b's instances one at a time with the manifest's own
	// arguments. The rest of the site has to be running for either to reach the
	// journal.
	deployment.StartSite(ctx, t, "node-b")
	manifest := harness.ReadManifest(t, node.BinaryPath)
	require.Equal(t, []string{"-instance", primaryInstance}, manifest.Primary.Args)
	require.NotNil(t, manifest.Standby)
	require.Equal(t, []string{"-instance", standbyInstance}, manifest.Standby.Args)

	// The machine's purpose is profile; the two launches carry the other kind
	// of role. One word for both axes is what the rename removed.
	require.Equal(t, "all-in-one", manifest.MachineProfile)

	// Both fixed instances name the Windows Service that should run them, and the
	// two differ: they share a host, so identical names would be an install
	// conflict the platform can catch and Windows cannot warn about in advance.
	require.NotEmpty(t, manifest.Primary.Service.Name)
	require.NotEmpty(t, manifest.Primary.Service.DisplayName)
	require.NotEmpty(t, manifest.Standby.Service.Name)
	require.NotEqual(t, manifest.Primary.Service.Name, manifest.Standby.Service.Name)

	launches := []struct {
		name   string
		launch harness.Launch
	}{
		{name: primaryInstance, launch: manifest.Primary},
		{name: standbyInstance, launch: *manifest.Standby},
	}
	// These subtests share one machine instance and sequentially start/stop it
	// with different arguments. They must never run concurrently with each other,
	// so do not add t.Parallel() inside this subtest loop.
	for _, process := range launches {
		t.Run(process.name, func(t *testing.T) {
			node.LaunchArgs = process.launch.Args
			node.Process = nil
			node.Start(ctx, t)
			harness.WaitForAPI(ctx, t, node)
			node.Stop()
		})
	}
}
