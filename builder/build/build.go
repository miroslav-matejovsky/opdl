package build

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/miroslav-matejovsky/opdl/builder/internal/blueprint"
	"github.com/miroslav-matejovsky/opdl/builder/internal/pack"
	"github.com/miroslav-matejovsky/opdl/builder/internal/resolve"
)

// Default paths and platform identity, matching the builder CLI flag defaults.
// They are relative to the builder module root, so the CLI resolves them the way
// it always has. A Go caller that builds from elsewhere sets the fields
// explicitly; the defaults only fill fields left empty.
const (
	defaultPlatform    = "opdl"
	defaultExamplesDir = "../examples"
	defaultPlatformDir = "../platform"
	defaultOutDir      = "../dist"
)

// Options is one build request: which blueprint to build, which platform source
// to compile, and where the packages go.
type Options struct {
	// ExamplesDir is the directory holding project blueprints.
	ExamplesDir string
	// Project is the blueprint directory name under ExamplesDir.
	Project string
	// Platform is the product-line identity stamped on descriptors.
	Platform string
	// PlatformDir is the platform module root to compile.
	PlatformDir string
	// OutDir is the directory packages are written to.
	OutDir string
	// GOARCH cross-compiles the machine binaries; empty means the host
	// architecture.
	GOARCH string
}

// withDefaults fills empty fields with the CLI defaults, leaving any field the
// caller set untouched.
func (o Options) withDefaults() Options {
	if o.Platform == "" {
		o.Platform = defaultPlatform
	}
	if o.ExamplesDir == "" {
		o.ExamplesDir = defaultExamplesDir
	}
	if o.PlatformDir == "" {
		o.PlatformDir = defaultPlatformDir
	}
	if o.OutDir == "" {
		o.OutDir = defaultOutDir
	}
	return o
}

// Result reports one machine's built package.
type Result struct {
	// Machine is the deployment machine the package is for.
	Machine string
	// Dir is the package directory.
	Dir string
	// Binary is the machine binary filename inside Dir.
	Binary string
	// SHA256 is the machine binary's checksum.
	SHA256 string
}

// Run loads and validates the blueprint, resolves the per-machine plan, and
// builds a deployment package for every machine. It returns one Result per
// machine in plan order.
//
// On a machine build failure it returns the results produced so far and an
// error naming the machine, so a caller can see what completed before the stop.
func Run(ctx context.Context, opts Options) ([]Result, error) {
	opts = opts.withDefaults()
	if opts.Project == "" {
		return nil, fmt.Errorf("no project given")
	}

	p, err := blueprint.Load(filepath.Join(opts.ExamplesDir, opts.Project))
	if err != nil {
		return nil, err
	}
	plan, err := resolve.Build(p, opts.Platform)
	if err != nil {
		return nil, err
	}
	packer, err := pack.New(opts.PlatformDir, opts.OutDir, opts.GOARCH)
	if err != nil {
		return nil, err
	}

	results := make([]Result, 0, len(plan.Machines))
	for _, d := range plan.Machines {
		res, err := packer.BuildMachine(ctx, d)
		if err != nil {
			return results, fmt.Errorf("build %s/%s/%s: %w", d.Project, d.Site, d.Machine, err)
		}
		results = append(results, Result{
			Machine: d.Machine,
			Dir:     res.Dir,
			Binary:  res.Binary,
			SHA256:  res.SHA256,
		})
	}
	return results, nil
}
