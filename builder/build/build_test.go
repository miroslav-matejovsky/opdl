package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/miroslav-matejovsky/opdl/builder/internal/pack"
	"github.com/stretchr/testify/require"
)

// TestRunRequiresBlueprintDir is the cheap guard: an empty request fails before
// any blueprint is loaded or anything is compiled, so it runs in every gate.
func TestRunRequiresBlueprintDir(t *testing.T) {
	_, err := Run(context.Background(), Options{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no blueprint directory given")
}

// TestOptionsDefaults checks the empty-field defaults match the CLI's, and that
// an explicit value is left alone.
func TestOptionsDefaults(t *testing.T) {
	filled := Options{BlueprintDir: "b"}.withDefaults()
	require.Equal(t, defaultPlatform, filled.Platform)
	require.Equal(t, defaultPlatformDir, filled.PlatformDir)
	require.Equal(t, defaultOutDir, filled.OutDir)

	custom := Options{BlueprintDir: "b", Platform: "custom", OutDir: "/tmp/out"}.withDefaults()
	require.Equal(t, "custom", custom.Platform)
	require.Equal(t, "/tmp/out", custom.OutDir)
}

// TestRunBuildsEveryMachine builds the minimal fixture end to end and checks what
// it produced: one package per machine, each holding its binary and the metadata
// files a self-describing package needs, with a checksum that matches the binary
// and a manifest that reflects the machine's standby policy.
//
// It compiles the platform, so it is skipped in -short mode (the unit gate) and
// runs in the integration gate. This is the automated form of driving the
// builder by hand: build a project, then read the packages back.
func TestRunBuildsEveryMachine(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build integration test in -short mode: it compiles the platform")
	}

	out := t.TempDir()
	results, err := Run(context.Background(), Options{
		BlueprintDir: filepath.Join("testdata", "minimal"),
		PlatformDir:  filepath.Join("..", "..", "platform"),
		OutDir:       out,
	})
	require.NoError(t, err)
	require.Len(t, results, 2, "the fixture has two machines")

	byMachine := make(map[string]Result, len(results))
	for _, r := range results {
		byMachine[r.Machine] = r
	}
	require.Contains(t, byMachine, "node-a")
	require.Contains(t, byMachine, "node-b")

	for _, machine := range []string{"node-a", "node-b"} {
		r := byMachine[machine]

		// The package sits where Run reported and holds the binary and the four
		// metadata files a self-describing package needs.
		require.Equal(t, filepath.Join(out, "buildtest", "solo", machine), r.Dir)
		for _, name := range []string{r.Binary, "deployment.json", "manifest.json", "release.json", "checksums.txt"} {
			require.FileExists(t, filepath.Join(r.Dir, name), "%s package missing %s", machine, name)
		}

		// The binary is real, and the checksum Run reported is the binary's own,
		// the same one the release metadata records.
		binary, err := os.ReadFile(filepath.Join(r.Dir, r.Binary))
		require.NoError(t, err)
		require.NotEmpty(t, binary, "%s binary is empty", machine)
		sum := sha256.Sum256(binary)
		require.Equal(t, hex.EncodeToString(sum[:]), r.SHA256, "%s reported checksum does not match its binary", machine)

		var release pack.Release
		readJSON(t, filepath.Join(r.Dir, "release.json"), &release)
		require.Equal(t, r.SHA256, release.BinarySHA256, "%s release metadata disagrees with the binary", machine)
		require.Equal(t, "windows", release.OS)

		var manifest pack.Manifest
		readJSON(t, filepath.Join(r.Dir, "manifest.json"), &manifest)
		require.Equal(t, machine, manifest.Machine)
		require.Equal(t, "test-node", manifest.MachineProfile)
		require.NotEmpty(t, manifest.Primary.Args, "%s manifest has no primary launch", machine)
	}

	// The standby policy the blueprint authored shows up in the manifests: node-a
	// opted out, node-b deploys a standby.
	var nodeA, nodeB pack.Manifest
	readJSON(t, filepath.Join(byMachine["node-a"].Dir, "manifest.json"), &nodeA)
	readJSON(t, filepath.Join(byMachine["node-b"].Dir, "manifest.json"), &nodeB)
	require.Nil(t, nodeA.Standby, "node-a opted out of a standby, so its manifest packages none")
	require.NotNil(t, nodeB.Standby, "node-b deploys a standby, so its manifest packages one")
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, v))
}
