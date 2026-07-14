// Command opdl is the builder CLI. It turns an authored blueprint into
// deployment packages, one per machine.
//
// Usage:
//
//	opdl validate <project>   load and validate a project blueprint
//	opdl plan     <project>   print the per-machine deployment descriptors
//	opdl build    <project>   compile the platform and assemble packages
//
// A <project> names a blueprint directory under -examples (default ../examples,
// so the tool runs from the builder module root). Path defaults assume that
// layout; override them with the flags on each subcommand.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/miroslav-matejovsky/opdl/builder/internal/blueprint"
	"github.com/miroslav-matejovsky/opdl/builder/internal/pack"
	"github.com/miroslav-matejovsky/opdl/builder/internal/resolve"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "opdl:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: opdl <validate|plan|build> [flags] <project>")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "validate":
		return cmdValidate(rest)
	case "plan":
		return cmdPlan(rest)
	case "build":
		return cmdBuild(rest)
	default:
		return fmt.Errorf("unknown command %q (want validate, plan, or build)", cmd)
	}
}

// cmdValidate loads a blueprint, which validates it, and reports success.
func cmdValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	examples := fs.String("examples", "../examples", "directory holding project blueprints")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := loadProject(*examples, fs.Arg(0))
	if err != nil {
		return err
	}
	fmt.Printf("ok: %s (%d sites)\n", p.Name, len(p.Sites))
	return nil
}

// cmdPlan prints the per-machine deployment descriptors as JSON. It touches no
// files and compiles nothing.
func cmdPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	examples := fs.String("examples", "../examples", "directory holding project blueprints")
	platform := fs.String("platform", "opdl", "product-line identity stamped on descriptors")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := loadProject(*examples, fs.Arg(0))
	if err != nil {
		return err
	}
	plan, err := resolve.Build(p, *platform)
	if err != nil {
		return err
	}
	out, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}

// cmdBuild resolves a plan and assembles a deployment package per machine.
func cmdBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	examples := fs.String("examples", "../examples", "directory holding project blueprints")
	platform := fs.String("platform", "opdl", "product-line identity stamped on descriptors")
	platformDir := fs.String("platform-dir", "../platform", "platform module root to compile")
	out := fs.String("out", "../dist", "directory to write packages to")
	goos := fs.String("goos", "", "target OS (empty means host)")
	goarch := fs.String("goarch", "", "target architecture (empty means host)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := loadProject(*examples, fs.Arg(0))
	if err != nil {
		return err
	}
	plan, err := resolve.Build(p, *platform)
	if err != nil {
		return err
	}

	packer, err := pack.New(*platformDir, *out, *goos, *goarch)
	if err != nil {
		return err
	}
	defer func() {
		if restoreErr := packer.Restore(); restoreErr != nil {
			fmt.Fprintln(os.Stderr, "opdl:", restoreErr)
		}
	}()

	ctx := context.Background()
	for _, d := range plan.Machines {
		res, err := packer.BuildMachine(ctx, d)
		if err != nil {
			return fmt.Errorf("build %s/%s/%s: %w", d.Project, d.Site, d.Machine, err)
		}
		fmt.Printf("built %s -> %s\n", d.Machine, res.Dir)
	}
	fmt.Printf("done: %d machine(s)\n", len(plan.Machines))
	return nil
}

// loadProject resolves a project name to its blueprint directory and loads it.
func loadProject(examplesDir, name string) (*blueprint.Project, error) {
	if name == "" {
		return nil, fmt.Errorf("no project given")
	}
	return blueprint.Load(filepath.Join(examplesDir, name))
}
