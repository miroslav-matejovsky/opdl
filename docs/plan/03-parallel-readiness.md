# Stage 3: make every scenario safe to run concurrently

## Goal

Remove every remaining reason a scenario could interfere with another one.
Parallelism stays off. The suite still runs serially at the end of this stage,
but nothing is left that would break if it did not.

Splitting this from stage 4 is deliberate. Turning on `t.Parallel()` and
fixing shared state at the same time produces failures that could be either,
and scenario failures are expensive to diagnose because each one is a build
plus several real processes.

## Audit

Each item below is either confirmed safe already or needs work.

### Already safe, keep it that way

- **Per-test filesystem.** After stage 2 every scenario works under its own
  `scenarios/.tmp/<TestName>/` root, keyed by top-level test name. Data,
  instance, and event directories all derive from it
  (`scenarios/harness_test.go:318`). Test names are unique, so no
  cross-scenario path exists inside one process. Two `go test` processes are a
  different matter: see stage 2 and `OPDL_SCENARIO_TMP`.
- **No process-wide chdir.** `buildProject` sets `cmd.Dir`
  (`harness_test.go:209`) instead of calling `os.Chdir`. A chdir anywhere in
  the harness would break parallelism outright. Worth a comment saying so, so
  it is not introduced later.
- **`projectFixtures` is read-only** (`harness_test.go:77`). It is written
  once at package init and only read after. Leave it that way.
- **Environment.** The dotnet scenario appends to `os.Environ()` for one child
  (`scenarios/dotnet_sdk_e2e_test.go:65`) rather than calling `os.Setenv`.
- **Buffers.** `syncBuffer` (`harness_test.go:413`) is mutex guarded, which is
  what makes reading a running machine's output safe under `-race`.
- **No `require.*` on a poll goroutine.** `submitRegistration` and
  `fetchRegistration` report errors instead of asserting, for the reason
  documented at `scenarios/registrationapi_test.go:107`. `observeListenerGap`
  (`scenarios/warm_standby_test.go:248`) and the condition in
  `waitForConnectionEvent` (`scenarios/four_machine_storage_test.go:142`) are
  clean too. Re-check as part of this audit and keep it clean.

### Needs work

1. **`TestManifestArgumentsMatchRuntime` subtests mutate shared state.**
   `scenarios/manifest_contract_test.go:28` runs two subtests that each write
   `node.launchArgs`, `node.output`, and `node.cmd` before starting the same
   machine. They must never become parallel with each other. The parent test
   can be parallel with other tests; its subtests cannot be parallel with each
   other. Add a comment stating the constraint at the subtest loop, because
   the next person adding `t.Parallel()` everywhere will otherwise add it here
   and get a confusing intermittent failure.

2. **`restart` registers a second cleanup.** `start` calls `t.Cleanup(m.stop)`
   (`harness_test.go:749`), and `restart` (`:755`) calls `start` again, so a
   restarted machine has two `stop` cleanups. `stop` is idempotent
   (`:768`), so this is harmless today. Under parallelism, cleanup ordering
   becomes the thing that decides whether a machine outlives its test, so make
   the registration explicit: register the cleanup once at `prepareMachine`
   rather than once per `start`.

3. **`startProcess` cleanup kills and waits.** `harness_test.go:856` kills the
   child and then waits for it. That is correct and must stay correct: a
   parallel test that returned while its child was still writing to a buffer
   would race the next test for CPU and file handles. Verify no path skips it.

4. **`dotnet test` builds a shared project.** `Opdl.Sdk.E2E.csproj` has one
   `obj/` and one `bin/`. Only `TestDotnetSDKEndToEnd` builds it, so there is
   no conflict today. Record the constraint: no second scenario may invoke
   `dotnet` on that project. If one is ever added, both must share a build
   done once in `TestMain`.

5. **`go run ./cmd/opdl` per scenario.** `buildProject` (`harness_test.go:207`)
   invokes `go run`, which compiles the builder before running it. Serially
   the compile is cached after the first scenario. In parallel, nine
   invocations start close together and contend on the build cache. Stage 4
   fixes this by compiling the builder once. Noted here because it is a
   parallelism cost, not a correctness problem.

## Changes

`scenarios/harness_test.go`
- Move `t.Cleanup(m.stop)` from `start` to `prepareMachine`.
- Comment on `buildProject` recording that the harness must never chdir.

`scenarios/manifest_contract_test.go`
- Comment on the subtest loop recording that these subtests share one machine
  and must stay serial.

No other production or scenario behavior changes in this stage.

## Verification

Serial correctness first:

- `task scenarios` passes.
- `go test -count=1 -shuffle=on ./...` in `scenarios` passes several times.
  Shuffle catches an ordering dependency between scenarios, which is the class
  of bug that parallelism would otherwise expose as a flake.
- `go test -count=1 -race ./...` passes. The suite drives subprocesses, so
  `-race` mostly covers the harness itself, which is exactly what is being
  changed here.

Then a concurrency preview without enabling `t.Parallel()`:

- Run two `go test` processes over the whole package at once, with
  `OPDL_SCENARIO_TMP` set to a separate root in the second. With stage 1 in
  place they should not collide on ports, and any shared filesystem or build
  assumption shows up here. The override is required rather than optional:
  without it the two runs empty each other's scratch directory, and the
  failure that produces looks nothing like a port collision.
- Run the two-machine and four-machine scenarios in two shells simultaneously
  using `-run`, again with separate scratch roots. This is the heaviest
  realistic pairing.

## Done when

- Shuffled and raced serial runs pass repeatedly.
- Two concurrent whole-package runs pass on separate scratch roots.
- Every item in the audit is either confirmed safe in a comment or fixed.
</content>
