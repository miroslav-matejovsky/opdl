package apispecifications

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// The dotnet SDK client is generated from api-specifications/openapi.yaml by Kiota,
// driven from here so the conformance-tests module owns every generated specification
// artifact. The generated code lives under sdk-dotnet; these constants pin the
// Kiota invocation. See contractPath for why this relative path works from both
// this package and conformance-tests/cmd.
var dotnetClientDir = filepath.Join("..", "..", "sdk-dotnet", "src", "Opdl.Sdk", "Client")

const (
	dotnetNamespace = "Opdl.Sdk.Client"
	dotnetClassName = "PlatformClient"
)

// generateDotnetClient regenerates the checked-in Kiota client under sdk-dotnet
// from the OpenAPI specification, unconditionally, so the SDK is always exactly
// what the current specification produces.
//
// Generation shells out to Kiota, so it needs Kiota installed; when it is not, this
// returns a SkipError rather than failing, since there is nothing this check can do
// about a missing tool.
func generateDotnetClient(ctx context.Context) error {
	kiota, err := exec.LookPath("kiota")
	if err != nil {
		return &SkipError{Reason: "kiota not installed; skipping dotnet client generation"}
	}

	cmd := exec.CommandContext(ctx, kiota, "generate",
		"--language", "CSharp",
		"--openapi", contractPath,
		"--output", dotnetClientDir,
		"--namespace-name", dotnetNamespace,
		"--class-name", dotnetClassName,
		"--clean-output",
		"--log-level", "Warning",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kiota generate failed: %w\n%s", err, out)
	}
	return os.Remove(filepath.Join(dotnetClientDir, ".kiota.log"))
}
