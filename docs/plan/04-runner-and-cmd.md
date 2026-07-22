# Stage 04: the runner and the command

Effort: M (about 0.5 to 1 day). Complexity: Med-High.

## Goal

Add `scenarios/internal/runner` and `scenarios/cmd/main.go` (a plain `cmd`
folder, no subfolder). The
runner turns a set of registered scenarios into a `[]testing.InternalTest` and
runs them through the standard test runner. The command parses flags and calls
the runner. There is no builder bootstrap: the harness builds in-process
(stage 03), so the command only has to run scenarios.

At the end of this stage the command builds and runs, driving zero or a trivial
set of scenarios. The real scenarios are wired in stage 05. Building the
plumbing first means it can be proven before any category depends on it.

`task scenarios` still uses `go test` at the end of this stage. The cutover to
`go run` happens in stage 05, together with deleting the old test files.

## The Scenario type and registry

In `internal/runner`:

```go
// Scenario is one black-box scenario: a name and the function that runs it.
type Scenario struct {
    Name string
    Func func(*testing.T)
}

// A Set is the scenarios contributed by one category package.
type Set struct {
    Package  string      // e.g. "nats"
    Scenarios []Scenario
}
```

Registration is explicit and pull-based rather than relying on `init()` side
effects, so the command controls order and the deadcode tool sees the edges.
Each category package exposes:

```go
func Scenarios() runner.Set
```

The command imports each category package and assembles `[]runner.Set`.

## Running through the standard test runner

The runner flattens the enabled sets into `[]testing.InternalTest`, prefixing
each name with its package so `-run` and output read naturally
(`nats/TestTwoMachineEventFabric`), and calls:

```go
m := testing.MainStart(deps, tests, nil, nil, nil)
os.Exit(m.Run())
```

`MainStart` provides real `*testing.T` values, so `t.Parallel()`, `t.Cleanup()`,
`t.Run()`, `-timeout`, `-parallel`, `-run`, and verbose output all work without
re-implementation. The scenario functions keep the exact bodies they have as
tests, minus the `Test` name prefix.

### The testDeps adapter

`MainStart`'s first parameter is the unexported `testing.testDeps` interface. It
can be satisfied from another package because interface satisfaction is
structural. Implement a minimal adapter:

- `MatchString(pat, str string) (bool, error)`: compile `pat` as a regexp and
  match `str`. This is what powers `-run`.
- Everything else (CPU/coverage/fuzz/test-log hooks): no-op returning zero
  values or nil.

The exact method set is Go-version-specific and its doc states the signature
"may change signature from release to release". The repo pins Go 1.26.5. Build
the adapter by letting the compiler report the required methods and implementing
each as a no-op except `MatchString`. Keep the adapter in one small file with a
comment naming the Go version it was written against.

### Fallback if testDeps is troublesome

`testing.RunTests(matchString func(pat, str string) (bool, error), tests
[]testing.InternalTest) bool` needs only a matcher and the test list, no
`testDeps`. It is simpler but does not parse flags or enforce `-timeout` on its
own. If the `testDeps` method set proves painful, use `RunTests` plus:

- `testing.Init()` and `flag.Parse()` before running, so `-test.v`,
  `-test.parallel`, and `-test.run` are honored.
- A watchdog: `context`/`time.AfterFunc` that prints running goroutines and
  exits non-zero at the timeout, since `RunTests` will not.

`MainStart` is the recommended path because it gives `-timeout` for free.

## The command

`cmd/main.go` responsibilities:

1. Define the command's own flags (stage 06 extends these):
   - `-parallel N` maps to `test.parallel`.
   - `-timeout D` maps to `test.timeout` (default 30m, matching the current
     `scenarios.ps1`).
   - `-run PATTERN` maps to `test.run`.
   - `-v` maps to `test.v`.
   - `-count N` maps to `test.count` (default 1; scenarios must not use cached
     results, which the current script enforces with `-count=1`).
   - `-platform-dir PATH` (optional) to override the platform module root the
     harness builds from (default `../platform`, since the task runs from
     `scenarios/`).
2. Translate its flags into the `test.*` flags the testing package registers.
   The simplest robust approach: build an `os.Args` slice of `-test.*=...` values
   and let `MainStart(...).Run()` parse them. Alternatively set the flag values
   after `testing.Init()`.
3. Assemble the enabled `[]runner.Set` and call the runner.

There is no builder bootstrap here. Stage 02 made the builder build flow a
public in-process call and stage 03 wired the harness to it, so the command has
no CLI to compile and no builder binary to locate. If `-platform-dir` is
provided, pass it to the harness (a `harness` config field or setter); otherwise
the harness uses its `../platform` default.

The `-short` short-circuit is gone. Under `go run` there is no `-short`; the
command always runs the black-box scenarios. Unit tests keep using `-short`
under `go test` in `task test`, untouched.

## Steps

1. Create `internal/runner` with `Scenario`, `Set`, the flatten-and-run
   function, and the `testDeps` adapter.
2. Create `cmd/main.go` with flags, flag translation, optional `-platform-dir`
   wiring, and a call into the runner with an empty or one-item set.
3. Leave `taskfile/scenarios.ps1` on `go test` for now, or optionally add a
   commented `go run ./cmd` line to be enabled in stage 05.

## Files touched

- New: `scenarios/internal/runner/runner.go`,
  `scenarios/internal/runner/deps.go`, `scenarios/internal/runner/doc.go`.
- New: `scenarios/cmd/main.go`.

## Verification

- `go build ./cmd` succeeds in `scenarios/`.
- `go run ./cmd -run x` runs, registers the trivial set, and exits 0
  (matches nothing). This proves `MainStart`, the `testDeps` adapter, and flag
  translation compile and execute.
- `go vet ./...` in `scenarios/` succeeds.
- `deadcode` now auto-discovers `scenarios` as a cmd module (it has `cmd/`). The
  script still adds `scenarios` explicitly; the duplicate is deduplicated and
  harmless until stage 07.
- `task fast` passes.

## Risks and notes

- The `testDeps` interface is the main risk. Contain it in one file, document
  the Go version, and keep the fallback in mind.
- Flag translation is fiddly. Prefer constructing `-test.*` args and letting the
  testing package parse them over poking flag values by hand.
- Keep the command thin. Category wiring is data (`[]runner.Set`), added in
  stage 05. Do not let scenario-specific logic leak into `main.go`.
