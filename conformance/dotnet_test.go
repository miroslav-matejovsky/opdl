package conformance_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The dotnet SDK client is generated from contracts/openapi.yaml by Kiota, driven
// from here so the conformance module owns every generated contract artifact. The
// generated code lives under sdk-dotnet; these constants pin the Kiota invocation.
var dotnetClientDir = filepath.Join("..", "sdk-dotnet", "src", "Opdl.Sdk", "Client")

const (
	dotnetNamespace = "Opdl.Sdk.Client"
	dotnetClassName = "PlatformClient"
)

// TestDotnetClient keeps the checked-in Kiota client in sync with the OpenAPI
// contract. With -update it regenerates the client under sdk-dotnet; otherwise it
// regenerates into a temporary directory and fails if the C# sources differ from
// what is checked in, catching a contract change that was not propagated to the
// SDK.
//
// Verification shells out to Kiota, so it needs Kiota installed and, because
// different Kiota versions emit different code, the same version that produced the
// checked-in client. When Kiota is missing or a different version, the check skips
// rather than reporting a false conflict; -update, which must produce real output,
// fails instead.
func TestDotnetClient(t *testing.T) {
	kiota, lookErr := exec.LookPath("kiota")

	if *updateContract {
		require.NoError(t, lookErr, "kiota not found on PATH; install it with: dotnet tool install -g Microsoft.OpenApi.Kiota")
		generateDotnetClient(t, kiota, dotnetClientDir)
		return
	}

	if lookErr != nil {
		t.Skip("kiota not installed; skipping dotnet client conformance check")
	}
	want := kiotaVersionFromLock(t)
	if got := installedKiotaVersion(t, kiota); got != want {
		t.Skipf("installed kiota %s differs from %s that generated the checked-in client; regenerate with: go test ./conformance -run TestDotnetClient -update", got, want)
	}

	tmp := t.TempDir()
	generateDotnetClient(t, kiota, tmp)
	require.Equal(t, csharpSources(t, dotnetClientDir), csharpSources(t, tmp),
		"sdk-dotnet client is stale; regenerate with: go test ./conformance -run TestDotnetClient -update")
}

// generateDotnetClient runs Kiota to generate the C# client from the OpenAPI
// contract into outDir, then removes Kiota's log file so it is not mistaken for a
// source artifact.
func generateDotnetClient(t *testing.T, kiota, outDir string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), kiota, "generate",
		"--language", "CSharp",
		"--openapi", contractPath,
		"--output", outDir,
		"--namespace-name", dotnetNamespace,
		"--class-name", dotnetClassName,
		"--clean-output",
		"--log-level", "Warning",
	)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "kiota generate failed:\n%s", out)
	require.NoError(t, os.Remove(filepath.Join(outDir, ".kiota.log")))
}

// csharpSources reads every generated C# file under dir keyed by its slash-style
// path relative to dir, with line endings normalized to LF. Normalization makes
// the comparison independent of the working tree's checkout style (the repo pins
// *.cs to CRLF) and of the line endings Kiota emits.
func csharpSources(t *testing.T, dir string) map[string]string {
	t.Helper()
	sources := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".cs") {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sources[filepath.ToSlash(rel)] = strings.ReplaceAll(string(content), "\r\n", "\n")
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, sources, "no generated C# sources found under %s", dir)
	return sources
}

// kiotaVersionFromLock reads the Kiota version recorded in the checked-in client's
// kiota-lock.json, the version that produced it.
func kiotaVersionFromLock(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dotnetClientDir, "kiota-lock.json"))
	require.NoError(t, err, "checked-in kiota-lock.json missing; generate the client with: go test ./conformance -run TestDotnetClient -update")
	var lock struct {
		KiotaVersion string `json:"kiotaVersion"`
	}
	require.NoError(t, json.Unmarshal(data, &lock))
	require.NotEmpty(t, lock.KiotaVersion)
	return lock.KiotaVersion
}

// installedKiotaVersion returns the version of the Kiota on PATH, dropping the
// build-metadata suffix so it compares equal to the lock file's plain version.
func installedKiotaVersion(t *testing.T, kiota string) string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), kiota, "--version").Output()
	require.NoError(t, err)
	version, _, _ := strings.Cut(strings.TrimSpace(string(out)), "+")
	return version
}
