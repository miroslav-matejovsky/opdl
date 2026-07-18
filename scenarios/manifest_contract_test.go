package scenarios

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestManifestSlotArgumentsMatchRuntime consumes the packaged launch contract
// without importing builder or platform internals. Both declared slot argument
// sets must start the packaged binary successfully.
func TestManifestSlotArgumentsMatchRuntime(t *testing.T) {
	ctx := t.Context()
	scenariosDir, err := filepath.Abs(".")
	require.NoError(t, err)
	outDir := t.TempDir()
	buildProject(ctx, t, filepath.Join(scenariosDir, "testdata"), outDir, "manifest-contract")

	deployment := prepareSite(t, outDir, t.TempDir(), "manifest-contract", "node")
	node := deployment.machine(t, "node")
	manifest := readManifest(t, node.binaryPath)
	require.Equal(t, "standby_then_active", manifest.ShutdownStrategy)
	require.Len(t, manifest.Slots, 2)
	require.Equal(t, []string{"a", "b"}, []string{manifest.Slots[0].Slot, manifest.Slots[1].Slot})

	for _, launch := range manifest.Slots {
		t.Run("slot "+launch.Slot, func(t *testing.T) {
			node.launchArgs = launch.Args
			node.output = &bytes.Buffer{}
			node.cmd = nil
			node.start(ctx, t)
			waitForAPI(ctx, t, node)
			node.stop()
		})
	}
}
