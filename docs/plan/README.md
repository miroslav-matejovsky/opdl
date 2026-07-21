# Scenario parallelization and harness simplification

A staged plan to run the black-box scenario suite in parallel and to move the
generic parts of its harness into tested `utils` packages.

Scope is `scenarios/` and the `utils` packages it consumes. No production code
under `platform/` or `builder/` changes.

## Reading order

| Stage | File | Outcome |
| --- | --- | --- |
| 1 | [01-port-allocator.md](01-port-allocator.md) | One process-wide port pool outside the ephemeral range. Removes the reserve-and-release lifecycle. |
| 2 | [02-scratch-directories.md](02-scratch-directories.md) | Scenario artifacts move to `scenarios/.tmp/<TestName>/`, replaced each run. |
| 3 | [03-parallel-readiness.md](03-parallel-readiness.md) | Every scenario is safe to run concurrently. Parallelism still off. |
| 4 | [04-enable-parallel.md](04-enable-parallel.md) | `t.Parallel()` enabled behind a machine budget. The payoff stage. |
| 5 | [05-process-runner.md](05-process-runner.md) | Three duplicate process lifecycles collapse into `utils/procrun`. |
| 6 | [06-waits-and-logscan.md](06-waits-and-logscan.md) | One polling primitive and pure log-parsing helpers. |
| 7 | [07-api-transport-and-docs.md](07-api-transport-and-docs.md) | Registration API transport deduplicated. Docs match the new harness. |

Stages 1 to 4 deliver parallelism. Stages 5 to 7 are the simplification work
and can be scheduled separately, but they are ordered after parallelism on
purpose: the refactors are easier to validate against a suite that already
runs concurrently, and each one is a behavior-preserving change that the
parallel suite can prove.

Stage 2 sits before the parallelism work rather than after it. Debugging a
parallel flake means reading the artifacts of a run that has already finished,
and those artifacts should be easy to find before that debugging starts, not
after. It also settles the filesystem model that stage 3 then audits.

## Why the current suite is serial

`scenarios.ps1` runs `gotestsum --format testname -- -count=1 ./...`. No
scenario calls `t.Parallel()`, so Go runs the nine top-level tests one at a
time.

| Test | Project | Machines |
| --- | --- | --- |
| `TestBuildAndRunSingleMachine` | scenario | 1 |
| `TestRestartRebuildsStateFromTheJournal` | scenario | 1 |
| `TestPlatformRefusesToStartWithoutItsJournalStorage` | scenario | 1 |
| `TestManifestArgumentsMatchRuntime` | manifest-contract | 1 |
| `TestWarmStandbyFailoverAndPreferredPrimary` | manifest-contract | 1 machine, 2 processes |
| `TestTwoMachineEventFabric` | two-machine | 2 |
| `TestTwoMachineRegistration` | two-machine | 2 |
| `TestDotnetSDKEndToEnd` | two-machine | 2, plus `dotnet test` |
| `TestFourMachineStorageTopologyAndFailure` | four-machine | 4 |

Fifteen platform processes and nine builder invocations, all sequential.

## The actual blocker

Ports, as expected, but not for the reason the harness comments assume.

`stageBlueprint` (`scenarios/harness_test.go:136`) reserves ports by listening
on `:0` and holds the listeners open through the build. `deploySite`
(`scenarios/harness_test.go:302`) reserves the API addresses and releases them
immediately at `harness_test.go:312`. Both then hand the numbers to a process
that binds them later.

Port `0` draws from the operating system ephemeral range. That range is also
what every outbound connection uses: NATS clients, the harness HTTP client,
and `dotnet test`. So between release and bind, the port can be taken by an
unrelated outbound socket in the same run. Serially the window is small and
rarely loses. Concurrently, with several scenarios reserving, releasing, and
binding at once, it is a routine collision.

Holding the listener longer does not fix this. Leaving the ephemeral range
does, and that is what stage 1 does.

## Second theme: the evidence is hidden

A scenario writes exactly what a developer needs in order to understand a
failure: the rendered blueprint it built from, the binaries and manifests the
builder produced, the per-machine journal, the process status files, and the
operational JSONL streams. All of it goes into `t.TempDir()`, so it lands
under a generated path in the system temporary directory and is usually
deleted before anyone looks.

`operationEvents` (`scenarios/harness_test.go:660`) exists only to paste one
of those directories into a failure message, which is a workaround for the
files being unreachable rather than a feature.

Stage 2 moves the lot to `scenarios/.tmp/<TestName>/`, replaced on each run.

## Third theme: harness complexity

`scenarios/harness_test.go` is 940 lines. Most of it is generic process and
polling machinery rather than anything about the distribution line:

- Three near-identical process lifecycles: `machine`
  (`harness_test.go:432`), `managedProcess` (`harness_test.go:475`), and
  `process` (`harness_test.go:829`). Each owns a `syncBuffer`, a
  `processtree.Owner`, a `done` channel, and an `err`.
- Two hand-rolled poll loops with the same shape: `waitStatus`
  (`harness_test.go:598`) and `waitForMarker` (`harness_test.go:807`).
- Two identical HTTP readiness polls: `waitForAPI`
  (`scenarios/registrationapi_test.go:218`) and `waitForManagedAPI`
  (`scenarios/warm_standby_test.go:229`).
- Four JSON-over-HTTP calls that differ only in method and shape, in
  `scenarios/registrationapi_test.go`.
- Five separate log scrapers across three files.

There is a coverage argument on top of the readability one. The fast gate runs
`-short ./...` per module, and the scenario `TestMain`
(`harness_test.go:47`) exits immediately under `-short`. Nothing in the
`scenarios` package is exercised by `task test`. The same logic living in
`utils` is unit tested on every fast gate. That is the reason to move the
generic parts out rather than to merely tidy them in place.

## Constraints that shape every stage

- `utils/testnet` is consumed by production module tests
  (`platform/internal/app/app_test.go:23`,
  `platform/internal/eventfabric/nats/nats_test.go:17`). Its existing
  `Reserve` and `ReserveOn` API must keep working unchanged. New behavior is
  added alongside it.
- Scenario helper code stays in `_test.go` files. A real package under
  `scenarios/` fails `task deadcode`.
- The `utils` module depends on `golang.org/x/sys` only. There is no
  `golang.org/x/sync`, so a weighted semaphore is hand-rolled.
- Blueprint ports are compiled into each machine binary, so two scenarios can
  never share a built artifact unless they also share ports, which would stop
  them running at the same time. Build caching across scenarios is therefore
  out of scope. Stage 4 reduces build cost a different way.
- A fixed scratch path and two concurrent `go test` processes cannot both
  work. Stage 2 chooses the fixed path, because a predictable location is the
  entire point, and provides `OPDL_SCENARIO_TMP` for the cases that need a
  second root. Parallelism inside one process is unaffected.
- Directories beginning with `.` are skipped by the Go tool, `golangci-lint`,
  and `deadcode`. That is why the scratch root is `.tmp` and not `tmp`:
  built binaries and rendered HCL sitting inside a module must be invisible to
  every gate in `task all`.
</content>
</invoke>
