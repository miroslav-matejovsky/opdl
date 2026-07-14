package scenarios

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBuildAndRunSingleMachine is the bare-minimum end-to-end scenario: drive
// the builder CLI to build the single machine in the "scenario" example
// blueprint, then run the resulting platform binary and check it starts and
// reports its configuration. Both are external processes; nothing here
// imports builder or platform Go code.
func TestBuildAndRunSingleMachine(t *testing.T) {
	ctx := context.Background()
	scenariosDir, err := filepath.Abs(".")
	require.NoError(t, err)
	blueprintsDir := filepath.Join(scenariosDir, "testdata")
	builderDir := filepath.Join(scenariosDir, "..", "builder")
	outDir := t.TempDir()

	// Flags must precede the positional project argument: Go's flag package
	// stops parsing flags at the first non-flag argument.
	build := exec.CommandContext(ctx, "go", "run", "./cmd/opdl", "build",
		"-examples", blueprintsDir, "-out", outDir, "scenario")
	build.Dir = builderDir
	output, err := build.CombinedOutput()
	require.NoError(t, err, "builder build failed:\n%s", output)

	binaryName := "node"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(outDir, "scenario", "local", "node", binaryName)
	require.FileExists(t, binaryPath)

	run := exec.CommandContext(ctx, binaryPath)
	runOutput, err := run.CombinedOutput()
	require.NoError(t, err, "platform binary failed:\n%s", runOutput)
	require.Contains(t, string(runOutput), "platform configuration")
}
