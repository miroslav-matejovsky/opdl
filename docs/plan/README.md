# Plan: refactor the scenarios module into a cmd + internal structure

## Goal

Turn `scenarios/` from a flat bag of `*_test.go` files driven by `go test`
into a proper Go program: a `cmd` entry point that runs the black-box
scenarios, `internal` packages for the shared harness and the helpers the
scenarios sit on, and one top-level package per scenario category.

Parallelism, timeouts, and scenario selection become responsibilities of the
scenarios command, not the taskfile. The command builds `*testing.T` values
through the standard testing runner and hands them to the scenario functions,
so scenarios keep `t.Parallel()`, `t.Cleanup()`, `t.Run()`, and `require`.

Unit tests for the scenarios packages stay as `_test.go` files and run under
`task test`. The black-box scenarios run under `task scenarios` via
`go run ./cmd/scenarios`.

## Scope and non-goals

- In scope: module structure, the command, the runner, relocating helpers,
  categorizing scenarios, per-package enable/disable, updating `deadcode.ps1`
  and the `scenarios` task.
- Not in scope: making the scenarios pass. `task scenarios` cannot run to green
  today and this plan does not try to fix that. Every stage is judged on
  compiling, listing, vetting, and (where noted) running the unit tests, not on
  scenario outcomes.
- Not in scope: changing `platform`, `builder`, or `conformance-tests` code,
  except that `scenarios` keeps depending on `utils/testnet` (see decisions).

## Decisions taken before this plan

Two questions were resolved with the requester up front.

1. utils strategy: `utils/` is shared, so "move all packages from utils" is not
   literally possible. Six packages are used only by scenarios and move into
   `scenarios/internal`: `procrun`, `processtree` (a dependency of `procrun`),
   `semaphore`, `waitfor`, `logscan`, `processinfo`. `utils/testnet` is used by
   `platform` too and stays in `utils`; scenarios keep importing it. It is the
   one deliberate exception to self-containment, alongside the builder CLI.
   The four platform-only packages (`atomicfile`, `jsonfields`, `stablehash`,
   `winmutex`) are untouched.

2. Categorization: six top-level scenario packages.

   | Scenario package        | Scenarios placed there                                              |
   | ----------------------- | ------------------------------------------------------------------ |
   | `scenarios/build`       | `TestBuildAndRunMinimumSite`, `TestManifestArgumentsMatchRuntime`  |
   | `scenarios/registration`| `TestTwoMachineRegistration`                                       |
   | `scenarios/nats`        | `TestTwoMachineEventFabric`, `TestFourMachineStorageTopologyAndFailure` |
   | `scenarios/resilience`  | `TestRestartRebuildsStateFromTheJournal`, `TestPlatformRefusesToStartWithoutItsJournalStorage` |
   | `scenarios/standby`     | `TestWarmStandbyFailoverAndPreferredPrimary`                       |
   | `scenarios/sdk`         | `TestDotnetSDKEndToEnd`                                            |

## Runner mechanism

The command runs scenarios through the standard library test runner rather than
re-implementing one. It builds a `[]testing.InternalTest{{Name, F}}` from the
enabled scenario packages and calls `testing.MainStart(deps, tests, nil, nil,
nil).Run()`. `MainStart` gives real `*testing.T` values with working
`t.Parallel()`, `-timeout` enforcement, `-parallel`, `-run`, and verbose output.

`testing.MainStart` takes an unexported `testDeps` interface and its doc warns
the signature "may change signature from release to release". The repo pins Go
1.26.5, so this is acceptable, but the exact method set is version-specific.
Stage 03 covers the adapter and a lower-risk fallback (`testing.RunTests` plus a
watchdog) if the `testDeps` method set proves troublesome.

## Target layout

```
scenarios/
  go.mod                         # still requires utils, now only for testnet
  cmd/
    main.go                      # flags, registry wiring, builder bootstrap, MainStart runner
  internal/
    harness/                     # site/machine/build + platform API client + fabric-config inspection
      site.go machine.go blueprint.go platformapi.go fabric.go doc.go
      testdata/project.hcl.tmpl  # embedded, no cwd dependency
    runner/                      # Scenario type, registry, testDeps adapter, Run()
      runner.go doc.go
    procrun/  processtree/  semaphore/  waitfor/  logscan/  processinfo/
                                 # moved verbatim from utils, with their _test.go
  build/        build.go manifest.go doc.go
  registration/ registration.go doc.go
  nats/         eventfabric.go storage.go doc.go
  resilience/   resilience.go doc.go
  standby/      standby.go doc.go
  sdk/          sdk.go doc.go
```

Top-level category packages are importable by `cmd`. `internal/*` is shared
private code. Category packages import `internal/harness` and register their
scenarios with `internal/runner`.

## Stage index

| Stage | File                              | What it delivers                                        | Effort | Complexity |
| ----- | --------------------------------- | ------------------------------------------------------- | ------ | ---------- |
| 01    | `01-relocate-utils.md`            | Six utils packages moved into `scenarios/internal`      | S      | Low        |
| 02    | `02-extract-harness.md`           | Shared harness extracted into `internal/harness`        | L      | High       |
| 03    | `03-runner-and-cmd.md`            | `internal/runner` and `cmd` skeleton                    | M      | Med-High   |
| 04    | `04-categorize-scenarios.md`      | Six scenario packages, registration wiring, task cutover| M-L    | Medium     |
| 05    | `05-enable-disable-flags.md`      | Per-package enable/disable selection                    | S      | Low-Med    |
| 06    | `06-deadcode-taskfile-docs.md`    | `deadcode.ps1`, docs, final gate reconciliation         | S-M    | Low        |

Effort scale: XS < S < M < L < XL. Rough total: 5 to 7 engineer-days.

## Ordering and why

Stages are ordered so the module keeps compiling and the existing gates keep
passing as long as possible.

- 01 moves leaf helpers first. Independent, low risk, verifiable alone.
- 02 turns the shared harness into a package while scenarios are still
  `_test.go` driven by `go test`. Biggest stage; isolating it keeps the churn
  in one place.
- 03 adds the runner and command with an empty or trivial scenario set, so the
  plumbing is provable before any scenario depends on it.
- 04 converts scenarios into registered functions, categorizes them, deletes the
  old root files, and flips `task scenarios` from `go test` to `go run`.
- 05 and 06 are additive polish: selection flags, then the deadcode script,
  docs, and a final pass over `task fast` and `task test`.

## Global risks

- The harness export in stage 02 is broad. Many unexported helpers and struct
  fields become part of a package API. Compile churn is large but mechanical.
- `testing.MainStart`'s `testDeps` is unexported and version-specific (stage 03).
- Relative paths to `../builder` and `../sdk-dotnet` assume the command runs
  from `scenarios/`. The `scenarios` task already sets `dir: scenarios`; stage 03
  adds override flags as hardening.
- The moved `_test.go` files (for example `procrun_test.go`) now run under the
  scenarios module's `task test` instead of the utils module's. They import only
  `testify` and the standard library, so no new external module is pulled in, but
  their `-short` behavior must be confirmed in their new home (stage 01).
