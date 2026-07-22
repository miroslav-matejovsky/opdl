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
- `deadcode.ps1`: runs `deadcode -test ./...` in modules that have a command or scenario tests,
  using command and test executables as reachability roots.
- `deadcode.ps1` and `arch.ps1` use installed `deadcode` and `go-arch-lint`
  binaries when available, falling back to `go run ...@latest` only when a tool
  is missing. This keeps checks usable without network access when tools are
  already installed.
- `test.ps1`: runs `gotestsum` in every module, teeing output to
  `.test-results/`.
- `clean.ps1`: removes build artifacts, test results, and output directories
  (`dist/`, `bin/`, etc.) both globally and per module, as well as stray `.exe`
  files. It retains `.gocache/` and `.lintcache/` so repeated fast runs reuse
  their workspace-local tool caches.
- `validate.ps1`: runs `opdl validate` for all project blueprints in
  `examples/` (or specific blueprints when names are passed as arguments).
- `integration-tests.ps1`: runs the platform's own tests without `-short`, so the ones
  that bind sockets and start real fabric members execute. This is the in-process
  half of the resilience gate.
- `scenarios.ps1`: runs the black-box scenario suite, which builds a deployment
  package and drives the built binary from outside. This is the out-of-process
  half. The suite is a program rather than a test binary, so the script only
  launches it and every knob is the command's: run `go run ./cmd -h` inside
  `scenarios/` for the list. It runs concurrently under a bounded load budget;
  when debugging, you can serialize it with `go run ./cmd -parallel 1` and narrow
  it with `go run ./cmd -run nats`.

  The two halves are separate scripts and separate tasks because they fail for
  different reasons and take very different times. A scenario failure means a
  packaged binary does not boot or does not recover, which no platform test can
  tell you; keeping them apart means one being unavailable does not take the
  other out of `task all` with it.

Run everything with `task all` from the repository root.

Task commands use `.gocache/` for Go build artifacts and `.lintcache/` for
golangci-lint. Both directories are ignored by Git. Every helper script checks
that these paths can be created and written before invoking a tool.
