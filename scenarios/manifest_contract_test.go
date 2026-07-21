package scenarios

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestManifestArgumentsMatchRuntime consumes the packaged launch contract
// without importing builder or platform internals.
func TestManifestArgumentsMatchRuntime(t *testing.T) {
	ctx := t.Context()
	outDir := filepath.Join(scenarioDir(t), "out")
	deployment := deploySite(ctx, t, outDir, filepath.Join(scenarioDir(t), "work"), "manifest-contract")
	node := deployment.machine(t, "node")
	manifest := readManifest(t, node.binaryPath)
	require.Equal(t, []string{"-instance", "primary"}, manifest.Primary.Args)
	require.NotNil(t, manifest.Standby)
	require.Equal(t, []string{"-instance", "standby"}, manifest.Standby.Args)

	launches := []struct {
		name   string
		launch launch
	}{
		{name: "primary", launch: manifest.Primary},
		{name: "standby", launch: *manifest.Standby},
	}
	for _, process := range launches {
		t.Run(process.name, func(t *testing.T) {
			node.launchArgs = process.launch.Args
			node.output = &syncBuffer{}
			node.cmd = nil
			node.start(ctx, t)
			waitForAPI(ctx, t, node)
			node.stop()
		})
	}
}
