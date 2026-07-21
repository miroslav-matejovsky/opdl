# Stage 4: enable parallel execution

## Goal

Run the scenario suite concurrently under a bounded load budget, and cut the
repeated builder compile. This is the stage that delivers the wall-clock win.

## Why a budget rather than plain `t.Parallel()`

`t.Parallel()` alone lets Go run up to `GOMAXPROCS` tests at once. That count
is meaningless here. A scenario is not one unit of work: it is a builder
invocation that compiles Go code, then between one and four platform
processes, each running an embedded NATS server with JetStream, each writing a
journal to disk.

Unbounded, an 8-core laptop would try to run the four-machine scenario, both
two-machine scenarios, and the dotnet scenario at once. That is eleven
platform processes plus a `dotnet test` plus concurrent compiles. The suite
would not deadlock, it would time out: `apiWaitTimeout` is 60 seconds
(`scenarios/registrationapi_test.go:30`) and `markerWaitTimeout` is 90 seconds
(`scenarios/harness_test.go:44`). Those bounds are generous for a machine
starting NATS and replaying a journal on an unloaded host. They are not
generous for one competing with ten others.

Raising the timeouts to absorb the load would be the wrong fix. It would make
every genuine failure take longer to report and would weaken the bounds that
catch a real startup regression. Bound the load instead and keep the timeouts
meaningful.

## Design

### Machine budget

A package-level weighted semaphore in the harness. Each scenario acquires
weight equal to the number of machines it deploys, before `deploySite`, and
releases in `t.Cleanup`.

```go
// scenarios/harness_test.go
// budget bounds the total number of platform processes the suite runs at once.
var budget = newBudget(max(2, runtime.GOMAXPROCS(0)/2))
```

`deploySite` acquires `len(projectFixtures[project])` and the caller never
sees it. That keeps the budget impossible to forget: a scenario that deploys a
site is charged for it automatically.

Weighting by machines rather than by test is what makes the budget behave.
Three single-machine scenarios cost the same as the four-machine one, which is
roughly true of the load they generate. A flat `-parallel N` cannot express
that.

`golang.org/x/sync/semaphore` is not available in the `utils` module, which
depends only on `golang.org/x/sys`. Hand-roll it: a mutex, a condition
variable, a capacity, and a current count. Roughly 40 lines. It belongs in
`utils/` rather than in the harness precisely because it is 40 lines of
concurrency that should be unit tested, and the fast gate covers `utils` while
it never runs anything in `scenarios`.

### Build concurrency

Separately bound builder invocations to 2. A build is CPU bound and short; a
running site is IO bound and long. Sharing one budget between them would
either starve builds or oversubscribe the CPU. Two independent limits are
simpler than one clever one.

### Compile the builder once

`buildProject` calls `go run ./cmd/opdl` (`scenarios/harness_test.go:207`),
which recompiles the builder command before every scenario. Compile it once in
`TestMain` into a temporary directory with `go build`, and have `buildProject`
exec that binary. Nine `go run` invocations become one `go build` plus nine
process starts, and the build-cache contention that concurrency would create
disappears with them.

This does not change what is being tested. The builder CLI is still driven as
an external command, exactly as a customer drives it.

### Enabling order

Do not enable everything at once. Add `t.Parallel()` in three passes, running
the full suite several times after each:

1. The single-machine scenarios: `TestBuildAndRunSingleMachine`,
   `TestRestartRebuildsStateFromTheJournal`,
   `TestPlatformRefusesToStartWithoutItsJournalStorage`,
   `TestManifestArgumentsMatchRuntime`.
2. `TestTwoMachineEventFabric`, `TestTwoMachineRegistration`, and
   `TestWarmStandbyFailoverAndPreferredPrimary`. The warm standby scenario is
   in this pass rather than the first because it runs two processes per
   machine and measures timing.
3. `TestFourMachineStorageTopologyAndFailure` and `TestDotnetSDKEndToEnd`, the
   two heaviest.

If a pass introduces a flake, the cause is inside a known set of four tests
instead of nine.

### Timing assertions under load

`TestWarmStandbyFailoverAndPreferredPrimary` records catch-up, promotion,
listener gap, and handover durations and logs them
(`scenarios/warm_standby_test.go:117`). It logs a baseline rather than
asserting a bound, so parallelism does not break it. It does make the numbers
noisier. Note in the log line that the values are load dependent, so nobody
later reads them as a benchmark.

### Runner flags

`taskfile/scenarios.ps1` currently runs
`gotestsum --format testname -- -count=1 ./...`.

- Add an explicit `-timeout`. The default is 10 minutes for the whole package
  and the current suite is close enough to it that a parallel run with a
  couple of retries could hit it. Set it to 30 minutes so the package timeout
  is a backstop rather than a participant.
- Add `-parallel` as a coarse outer bound, matching the budget's intent. The
  budget is the real control; this stops Go from starting a large number of
  tests that immediately block on it.
- Keep `-count=1`. Cached scenario results hide changes to fencing, signals,
  listeners, and child-process cleanup, as the script's comment already says.

## Changes

- `utils/semaphore/` (new): weighted semaphore, `Acquire(ctx, n)` and
  `Release(n)`, with tests for weighting, blocking, FIFO fairness, and context
  cancellation.
- `scenarios/harness_test.go`: package-level machine budget and build limit;
  `deploySite` acquires and registers the release; `TestMain` builds the
  builder binary once; `buildProject` runs it.
- Every scenario file: `t.Parallel()` in three passes.
- `taskfile/scenarios.ps1`: `-timeout` and `-parallel`.
- `scenarios/warm_standby_test.go`: note load dependence on the baseline log.

## Risks

- **Flakes that were always latent.** Parallelism does not usually create
  races, it exposes them. A scenario that fails here was probably marginal
  serially too. Treat a new flake as a real defect in the harness or the
  platform before treating it as a parallelism problem.
- **Diagnostics interleave.** `gotestsum --format testname` attributes output
  per test, and the failure diagnostics are attached to assertions rather than
  printed as they go, so this is mostly handled. Confirm a failure message is
  still readable by forcing one.
- **Budget tuning is host specific.** `GOMAXPROCS/2` is a starting point.
  Measure and adjust once. Do not add configuration for it before there is a
  second host that needs a different value.

## Verification

- Record the serial baseline before this stage: total suite wall clock and
  per-test durations from `gotestsum`.
- After each enabling pass, run the full suite at least three times.
- Compare wall clock against the baseline. The expected shape is that the
  suite approaches the duration of its longest single scenario plus the
  serialized build time, not that it divides by the core count.
- Run once with the budget set to 1 to confirm the suite still passes fully
  serialized. That keeps a debugging path open for whoever hits a flake later.

## Done when

- Every scenario calls `t.Parallel()` except the subtests noted in stage 3.
- Suite wall clock is measurably below the recorded baseline.
- Three consecutive full runs pass, including one with `-race`.
- Setting the budget to 1 still passes.
</content>
