package scenarios

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestManifestArgumentsMatchRuntime consumes the packaged launch contract
// without importing builder or platform internals.
func TestManifestArgumentsMatchRuntime(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	outDir := filepath.Join(scenarioDir(t), "out")
	deployment := deploySite(ctx, t, outDir, filepath.Join(scenarioDir(t), "work"), "manifest-contract")
	node := deployment.machine(t, "node")
	manifest := readManifest(t, node.binaryPath)
	require.Equal(t, []string{"-instance", "primary"}, manifest.Primary.Args)
	require.NotNil(t, manifest.Standby)
	require.Equal(t, []string{"-instance", "standby"}, manifest.Standby.Args)

	// The machine's purpose is profile; the two launches carry the other kind
	// of role. One word for both axes is what the rename removed.
	require.Equal(t, "all-in-one", manifest.Profile)

	// Both fixed instances name the Windows Service that should run them, and the
	// two differ: they share a host, so identical names would be an install
	// conflict the platform can catch and Windows cannot warn about in advance.
	require.NotEmpty(t, manifest.Primary.Service.Name)
	require.NotEmpty(t, manifest.Primary.Service.DisplayName)
	require.NotEmpty(t, manifest.Standby.Service.Name)
	require.NotEqual(t, manifest.Primary.Service.Name, manifest.Standby.Service.Name)

	launches := []struct {
		name   string
		launch launch
	}{
		{name: "primary", launch: manifest.Primary},
		{name: "standby", launch: *manifest.Standby},
	}
	// These subtests share one machine instance and sequentially start/stop it
	// with different arguments. They must never run concurrently with each other,
	// so do not add t.Parallel() inside this subtest loop.
	for _, process := range launches {
		t.Run(process.name, func(t *testing.T) {
			node.launchArgs = process.launch.Args
			node.Process = nil
			node.start(ctx, t)
			waitForAPI(ctx, t, node)
			node.stop()
		})
	}
}
