# Stage 02: expose the builder build flow as a public package

Effort: S-M (about 0.5 to 1 day). Complexity: Low-Med.

## Goal

Add a public builder package that runs the build flow through one function, so
callers do not need the CLI. The builder command and the scenarios harness both
call it. This replaces the harness's current approach of compiling the builder
CLI into a temp binary and driving it with `exec`.

This stage stands alone: after it, the builder still builds through
`task build`, `task validate`, and `task plan` exactly as before, because the
command is refactored to call the new package rather than duplicate the flow.

## Why this is now in scope

The original constraint was that scenarios drive the builder only as a
subprocess. That constraint is lifted: scenarios may import public builder
packages and build in-process. The builder already exports `builder/deployment`
(imported cross-module by `conformance-tests`), so a public build entry point
fits the existing shape. The build flow itself lives in
`builder/internal/{blueprint,resolve,pack}` and is not importable from another
module; this stage gives it a public door.

## Current flow to wrap

`builder/cmd/main.go` `cmdBuild` does, in order:

```go
p, err := blueprint.Load(filepath.Join(examplesDir, project))   // *blueprint.Project
plan, err := resolve.Build(p, platform)                          // *resolve.Plan, .Machines []deployment.Descriptor
packer, err := pack.New(platformDir, out, goarch)                // *pack.Packer
for _, d := range plan.Machines {
    res, err := packer.BuildMachine(ctx, d)                      // *pack.Result{Dir, Binary, SHA256}
}
```

All three helpers are internal to the builder module. A public package in the
same module may import them.

## New public package

Create `builder/build/build.go`:

```go
package build

// Options is one build request: which blueprint, which platform source, where
// output goes.
type Options struct {
    ExamplesDir string // directory holding project blueprints
    Project     string // blueprint directory name under ExamplesDir
    Platform    string // product-line identity stamped on descriptors; default "opdl"
    PlatformDir string // platform module root to compile
    OutDir      string // directory to write packages to
    GOARCH      string // target architecture; empty means host
}

// Result reports one machine's built package.
type Result struct {
    Machine string
    Dir     string
    Binary  string
    SHA256  string
}

// Run loads and validates the blueprint, resolves the per-machine plan, and
// builds a deployment package for every machine. It is the whole build flow the
// CLI runs, exposed for in-process callers such as the scenarios harness.
func Run(ctx context.Context, opts Options) ([]Result, error)
```

`Run` applies the same defaults the CLI flags used (`Platform` defaults to
`"opdl"`, `PlatformDir` to the platform module root, `OutDir` and `ExamplesDir`
as given). It imports `builder/internal/{blueprint,resolve,pack}` and
`builder/deployment`.

Add `builder/build/doc.go` describing the package as the public build entry
point.

## Refactor the command to use it

Change `builder/cmd/main.go` `cmdBuild` to parse its flags and then call
`build.Run(ctx, build.Options{...})`, printing the results as it does today
(`built <machine> -> <dir>`, `done: N machine(s)`). The command keeps owning
flag parsing and output; the flow moves into the package. `cmdValidate` and
`cmdPlan` can stay as they are, or optionally gain public wrappers later; only
build is needed for scenarios.

Doing this keeps one code path. The CLI and the harness build the same way,
which is the property the scenarios rely on.

## go.mod and workspace

- No new module dependency for the builder: `build` imports only builder-local
  packages and the standard library.
- The scenarios dependency on this package is added in stage 03 (harness), where
  `scenarios/go.mod` gains `require builder` and `replace builder => ../builder`.
- No import cycle: `scenarios -> builder -> utils`, and `scenarios -> utils`
  (testnet). `utils` imports neither, so the graph stays acyclic.

## Steps

1. Create `builder/build/build.go` with `Options`, `Result`, and `Run`.
2. Create `builder/build/doc.go`.
3. Refactor `cmdBuild` in `builder/cmd/main.go` to call `build.Run`.
4. Run `task tidy` (no dependency change expected; confirm clean).

## Files touched

- New: `builder/build/build.go`, `builder/build/doc.go`.
- Edited: `builder/cmd/main.go` (`cmdBuild` delegates to `build.Run`).

## Verification

- `go build ./...` and `go vet ./...` in `builder/` succeed.
- `task build -- <a real example project>` produces the same package tree as
  before (binary, `deployment.json`, `manifest.json`, `release.json`,
  `checksums.txt`).
- `task validate` and `task plan` still work.
- `task fast` passes. `deadcode` sees `build.Run` reached from `builder/cmd`, so
  it is not reported.

## Risks and notes

- Low risk. The flow is unchanged; it moves behind a public function and the
  command calls it.
- Keep `Run` free of CLI concerns (no flag parsing, no `os.Exit`, no printing).
  Return `[]Result` and an error; let callers render.
- `pack.BuildMachine` still shells out to `go build` per machine. Building
  in-process from the scenarios binary does not change that; it only removes the
  extra step of compiling the builder CLI first. Concurrency bounding stays the
  harness's job (stage 03).
- Do not expose `internal/pack.Manifest` here unless a caller needs it. The
  scenarios keep re-declaring the manifest JSON shape as a black-box contract
  check (see stage 03), which is deliberate.
