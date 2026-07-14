// Command conformance verifies that the independent representations of a shared
// contract stay compatible across the workspace's modules, by regenerating the
// specification artifacts from the code that owns them.
//
// It always regenerates rather than merely checking for staleness, so the
// artifacts it produces are always exactly what the current source describes; run
// it again after changing the platform API or deployment descriptors and check
// `git diff` (or a CI step that does the same) to see what moved.
//
// It runs the checks in api-specifications and deployment-descriptors, the
// packages that own the conformance logic. Run it directly:
//
//	go run ./conformance/cmd
//
// The same logic also runs via go test (main_test.go in this package), which is
// how it is wired into the workspace's regular test suite: go test
// ./conformance/cmd.
package main

import (
	"context"
	"fmt"
	"os"

	apispecifications "github.com/miroslav-matejovsky/opdl/conformance/api-specifications"
	deploymentdescriptors "github.com/miroslav-matejovsky/opdl/conformance/deployment-descriptors"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "conformance:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	if err := apispecifications.Run(ctx); err != nil {
		return err
	}
	return deploymentdescriptors.Run()
}
