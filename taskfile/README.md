# taskfile

Helper scripts backing the root `Taskfile.yml`. They exist because OPDL is a Go
workspace of several modules, and most checks have to run in each module rather
than once at the root.

- `modules.ps1`: shared helper, dot-sourced by the others. Defines `$RepoRoot`,
  the module list (`$Modules`), the cmd-bearing modules (`$CmdModules`), and
  `Invoke-PerModule`, which runs a script block in each module and fails fast on
  the first non-zero exit.
- `tidy.ps1`, `vet.ps1`, `fmt.ps1`, `lint.ps1`: run `go mod tidy`, `go vet`,
  `go fmt`, and `golangci-lint` in every module.
- `deadcode.ps1`: runs `deadcode -test ./...` in modules that have a command,
  using command and test executables as reachability roots.
- `test.ps1`: runs `gotestsum` in every module, teeing output to
  `.test-results/`.
- `clean.ps1`: removes build artifacts, test results, and output directories
  (`dist/`, `bin/`, etc.) both globally and per module, as well as stray `.exe`
  files.
- `validate.ps1`: runs `opdl validate` for all project blueprints in
  `examples/` (or specific blueprints when names are passed as arguments).
- `scenarios.ps1`: runs platform integration tests and the black-box scenario
  suite.

Run everything with `task all` from the repository root.
