# Stage 05: categorize scenarios into six packages and cut over the task

Effort: M-L (about 1 to 1.5 days). Complexity: Medium.

## Goal

Convert the eight scenario `*_test.go` files in `package scenarios` into six
top-level packages, each holding regular `.go` files whose scenario functions
are registered with the runner. Delete the old root files. Flip
`taskfile/scenarios.ps1` from `go test` to `go run ./cmd`.

The work is mostly mechanical because stage 03 already made the harness an
imported package. Each scenario body stays the same; what changes is where it
lives, that it is a regular function instead of a `Test` function, and that it is
registered.

## Package assignments

| Package                  | Source file(s)                          | Scenario functions                                        | Category-local helpers to move with them |
| ------------------------ | --------------------------------------- | --------------------------------------------------------- | ---------------------------------------- |
| `scenarios/build`        | `build_and_run_test.go`, `manifest_contract_test.go` | `BuildAndRunMinimumSite`, `ManifestArgumentsMatchRuntime` | none                                     |
| `scenarios/registration` | `two_machine_registration_test.go`      | `TwoMachineRegistration`                                  | none                                     |
| `scenarios/nats`         | `two_machine_eventfabric_test.go`, `four_machine_storage_test.go` | `TwoMachineEventFabric`, `FourMachineStorageTopologyAndFailure` | `journalOf`, `requireReported`, `lastFabricConfig`, `waitForConnectionEvent`, `proposeEventually` |
| `scenarios/resilience`   | `resilience_test.go`                    | `RestartRebuildsStateFromTheJournal`, `PlatformRefusesToStartWithoutItsJournalStorage` | none                     |
| `scenarios/standby`      | `warm_standby_test.go`                  | `WarmStandbyFailoverAndPreferredPrimary`                 | `waitForManagedAPI`, `observeListenerGap`, `assertSharedEndpoints` |
| `scenarios/sdk`          | `dotnet_sdk_e2e_test.go`                | `DotnetSDKEndToEnd`                                       | `pendingObservedMarker` const            |

`ParseFabricConfigs`/`FabricConfig` already moved to `internal/harness` in stage
02, so both `nats` and `standby` reach them through the harness. Category-local
helpers stay unexported inside their package.

## Converting a scenario function

For each `func TestX(t *testing.T)`:

1. Rename to `func X(t *testing.T)` (drop the `Test` prefix; the runner supplies
   the display name). Keep the body verbatim, now calling `harness.` for the
   shared surface.
2. Keep `t.Parallel()`, `t.Cleanup()`, `t.Run()`, `require`, `t.Context()`.
   These work under `MainStart` exactly as under `go test`.
3. The functions live in regular `.go` files, not `_test.go`, so the command can
   import and register them.

Each package gets a registration function:

```go
package nats

import "github.com/miroslav-matejovsky/opdl/scenarios/internal/runner"

func Scenarios() runner.Set {
    return runner.Set{
        Package: "nats",
        Scenarios: []runner.Scenario{
            {Name: "TwoMachineEventFabric", Func: TwoMachineEventFabric},
            {Name: "FourMachineStorageTopologyAndFailure", Func: FourMachineStorageTopologyAndFailure},
        },
    }
}
```

## Wiring the command

`cmd/main.go` imports the six packages and assembles the set list:

```go
sets := []runner.Set{
    build.Scenarios(),
    registration.Scenarios(),
    nats.Scenarios(),
    resilience.Scenarios(),
    standby.Scenarios(),
    sdk.Scenarios(),
}
```

Order here is the default run order (parallelism aside). Stage 05 adds filtering
over this list.

## Doc files

Each package gets a `doc.go`. Split the current root `doc.go` prose by subject:

- `build/doc.go`: the build-and-run and manifest-contract narrative.
- `nats/doc.go`: the event-fabric, storage-topology, and "where the NATS ports
  come from" narrative.
- `registration/doc.go`: the two-machine acceptance-barrier narrative.
- `resilience/doc.go`: restart and refuse-to-start.
- `standby/doc.go`: warm standby, failover, preferred-primary.
- `sdk/doc.go`: the .NET end-to-end narrative and the marker handshake.

Concurrency, artifact layout, and evidence prose that describe the harness move
to `internal/harness/doc.go` (done in stage 03); reference it from the package
docs rather than duplicating.

## Task cutover

Rewrite `taskfile/scenarios.ps1` to run the command:

```powershell
. (Join-Path $PSScriptRoot "modules.ps1")
Write-Host "--- scenarios ---"
Push-Location (Join-Path $RepoRoot "scenarios")
try {
    go run ./cmd -timeout 30m -parallel $([Environment]::ProcessorCount)
    if ($LASTEXITCODE -ne 0) { throw "scenarios failed (exit $LASTEXITCODE)" }
}
finally { Pop-Location }
Write-Host "scenarios done"
```

Notes:
- `dir` stays `scenarios/` so the harness's default `../platform` build path and
  the SDK scenario's `../sdk-dotnet` path resolve, unless stage 04's
  `-platform-dir` flag is used instead. The builder is no longer a path here; it
  is built in-process (stages 02 and 03).
- `-count=1` behavior moves into the command default (stage 04), so caching is
  off without a flag here.
- `gotestsum` was formatting `go test` output. Under `go run`, the testing
  runner's own `-v` output is what prints. If a nicer format is wanted later,
  the command can emit test2json and pipe through `gotestsum --raw-command`;
  that is optional and not required by this plan.

## Steps

1. Create the six package directories with their scenario `.go` files and
   `Scenarios()` registration functions.
2. Move category-local helpers into their owning package.
3. Add each `doc.go`.
4. Wire the six `Scenarios()` calls into `main.go`.
5. Delete the eight old `*_test.go` scenario files and the old root `doc.go`
   (keep a minimal root `doc.go` only if the `scenarios` root package still
   needs one; after this stage the root has no Go files, so no root package
   remains).
6. Rewrite `taskfile/scenarios.ps1` to `go run ./cmd`.

## Files touched

- New: `scenarios/{build,registration,nats,resilience,standby,sdk}/*.go`.
- Edited: `scenarios/cmd/main.go` (register the six sets).
- Edited: `taskfile/scenarios.ps1`.
- Deleted: the eight root `*_test.go` scenario files and the root `doc.go`.

## Verification

- `go build ./...` and `go vet ./...` in `scenarios/` succeed.
- `go run ./cmd -run x` lists and matches nothing, exits 0.
- `go run ./cmd -run BuildAndRun` starts the one scenario. It may fail
  on its assertions (scenarios are not expected to pass), but it must start,
  compile, and reach real scenario logic rather than a wiring error.
- `deadcode`: every category package is reachable from `main.go`; the scenario
  functions are reachable through `Scenarios()`; category-local helpers are
  reachable through their scenario. No orphans.
- `task fast` passes. `task scenarios` now invokes the command (and is still not
  expected to pass its scenarios).

## Risks and notes

- Watch for helpers used by two categories that were not lifted to the harness
  in stage 03. `ParseFabricConfigs` is the known one and is already in the
  harness. If another shared helper surfaces, lift it to `internal/harness`
  rather than duplicating it.
- After deleting the root test files, the `scenarios` root directory has no `.go`
  files. That is fine: the module is defined by `go.mod`, and packages live in
  subdirectories.
- `-parallel` default: the current suite bounds concurrency with its own machine
  budget in the harness (`semaphore`), independent of `test.parallel`. Keep the
  harness budget; `test.parallel` only bounds how many `t.Parallel()` scenarios
  run at once. Both limits coexist as before.
