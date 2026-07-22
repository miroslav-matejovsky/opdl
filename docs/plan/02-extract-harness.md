# Stage 02: extract the shared harness into internal/harness

Effort: L (about 1.5 to 2 days). Complexity: High.

## Goal

Turn the shared harness, which today lives in `harness_test.go` and
`registrationapi_test.go` as unexported code in `package scenarios`, into a
real package `scenarios/internal/harness` with an exported API. The scenario
files stay as `*_test.go` in `package scenarios` for now and call the harness
through its new exported surface. Everything still runs through `go test`.

This is the largest stage. It converts roughly forty unexported helpers and
several struct types into a package API, and it changes how the blueprint
template is loaded. Isolating it keeps that churn in one place, before the
runner and categorization stages depend on it.

## Why one package, not several

The site/machine/build harness and the platform API client are tightly coupled:
the client functions operate on a `*Machine` (its URL, its logs). The
fabric-config inspection (`parseFabricConfigs`) is shared between the `nats` and
`standby` scenarios. Splitting these into two or three internal packages is
possible but adds cross-package plumbing for no present benefit. Keep one
`internal/harness` package. A later split into `harness` plus `platformapi` is
noted as optional future work.

## New package contents

Create `scenarios/internal/harness/` with these files (names are a suggestion;
the split by concern is what matters):

- `site.go`: `Site`, `DeploySite`, `Site.Machine`, `Site.StartSite`,
  `Site.StartTogether`, `storageMachines` (exported `StorageMachines`),
  fixtures, blueprint staging.
- `machine.go`: `Machine`, `ManagedProcess`, lifecycle methods, status reading,
  diagnostics.
- `blueprint.go`: fixture model, port reservation, template rendering.
- `platformapi.go`: the HTTP client surface from `registrationapi_test.go`.
- `fabric.go`: `FabricConfig`, `ParseFabricConfigs`, `fabricConfigPrefix`,
  moved out of `warm_standby_test.go` because two categories need it.
- `doc.go`: the package documentation, adapted from the current `doc.go`.
- `testdata/project.hcl.tmpl`: moved from `scenarios/testdata/`, embedded.

## Exported API to design

Every identifier a scenario file references across the package boundary must be
exported. The table below is the cross-package surface, grouped by source. Names
on the right are the proposed exported names.

Site and build (from `harness_test.go`):

| Current (unexported)          | Exported                         |
| ----------------------------- | -------------------------------- |
| `deploySite`                  | `DeploySite`                     |
| `site`                        | `Site`                           |
| `site.machine`                | `Site.Machine`                   |
| `site.startSite`              | `Site.StartSite`                 |
| `site.startTogether`          | `Site.StartTogether`             |
| `site.machines` field         | `Site.Machines`                  |
| `storageMachines`             | `StorageMachines`                |
| `machineBinary`               | `MachineBinary` (or keep internal if only harness uses it) |
| `readManifest`                | `ReadManifest`                   |
| `packageManifest`, `launch`, `winService` | `PackageManifest`, `Launch`, `WinService` |
| `scenarioSite` const          | `Site` fixed value, keep internal or expose as needed |

Machine (from `harness_test.go`):

| Current                       | Exported                         |
| ----------------------------- | -------------------------------- |
| `machine`                     | `Machine`                        |
| `machine.start`               | `Machine.Start`                  |
| `machine.restart`             | `Machine.Restart`                |
| `machine.stop`                | `Machine.Stop`                   |
| `machine.startManaged`        | `Machine.StartManaged`           |
| `machine.waitStatus`          | `Machine.WaitStatus`             |
| `machine.running`             | `Machine.Running` (note it shadows the embedded `Process.Running`; keep the method name distinct, for example `IsRunning`, to avoid confusion) |
| `machine.exited`              | `Machine.Exited`                 |
| `machine.logs`                | `Machine.Logs` (shadows embedded `Process.Logs`; pick a distinct name such as `Output`) |
| `machine.wait`                | `Machine.Wait` (same shadowing caution) |
| `machine.readStatus`          | keep internal unless a scenario needs it (none do directly) |
| `machine.statusPath`          | keep internal                    |
| `machine.binaryPath` field    | `Machine.BinaryPath`             |
| `machine.url` field           | `Machine.URL`                    |
| `machine.name` field          | `Machine.Name`                   |
| `machine.launchArgs` field    | `Machine.LaunchArgs`             |
| `machine.sockets` field       | `Machine.Sockets`                |
| `machine.standby` field       | `Machine.Standby`                |
| `machine.workDir` field       | keep internal or `Machine.WorkDir` |
| `sockets` type + fields       | `Sockets` with `DataDir`, `EventDir`, `API`, `Client`, `Cluster` |
| `managedProcess`, `.role`     | `ManagedProcess`, `.Role`        |
| `managedProcess.stopGracefully` | `ManagedProcess.StopGracefully` |
| `processStatus`               | `ProcessStatus`                  |

Diagnostics and waiting (from `harness_test.go`):

| Current                       | Exported                         |
| ----------------------------- | -------------------------------- |
| `diagnose`                    | `Diagnose`                       |
| `diagnostics`                 | `Diagnostics`                    |
| `waitForMarker`               | `WaitForMarker`                  |
| `waitFor`                     | `WaitFor`                        |
| `diagStringer`                | `DiagStringer` or keep internal (only harness constructs it) |
| `appended`, `lazily`          | keep internal unless a scenario needs them (none do) |
| `markerPollInterval`, `markerWaitTimeout` consts | export if a scenario reads them (none do; keep internal) |

Platform API client (from `registrationapi_test.go`):

| Current                       | Exported                         |
| ----------------------------- | -------------------------------- |
| `proposalAccepted`            | `ProposalAccepted`               |
| `registration`                | `Registration`                   |
| `platformInstance`            | `PlatformInstance`               |
| `conflict`                    | `Conflict`                       |
| `registration.instance`       | `Registration.Instance`          |
| `propose`                     | `Propose`                        |
| `getRegistration`             | `GetRegistration`                |
| `listRegistrations`           | `ListRegistrations`              |
| `listConflicts`               | `ListConflicts`                  |
| `submitRegistration`          | `SubmitRegistration`             |
| `fetchRegistration`           | `FetchRegistration`              |
| `waitForAPI`                  | `WaitForAPI`                     |
| `waitForRegistration`         | `WaitForRegistration`            |
| `waitForRegistrationStatus`   | `WaitForRegistrationStatus`      |
| `confirmedBy`                 | `ConfirmedBy`                    |
| `getJSON`, `postJSON`         | keep internal                    |
| `lastPoll`                    | keep internal                    |
| `apiWaitTimeout`, `apiPollInterval` | `APIWaitTimeout` if scenarios read it (warm standby uses `apiWaitTimeout` in a `time.After`); export as `APIWaitTimeout` |

Fabric config (moved from `warm_standby_test.go`, shared by nats and standby):

| Current                       | Exported                         |
| ----------------------------- | -------------------------------- |
| `fabricConfig`                | `FabricConfig` with exported fields (`Endpoint`, `Binds`, `Cluster`, `Servers`, `Routes`, `Storage`) |
| `parseFabricConfigs`          | `ParseFabricConfigs`             |
| `fabricConfigPrefix`          | keep internal                    |

Helpers that a single category owns are not moved here. They move with their
scenario in stage 04:

- `journalOf`, `requireReported`: only `nats` uses them.
- `lastFabricConfig`, `waitForConnectionEvent`, `proposeEventually`: only `nats`
  (`four_machine`). They call exported harness functions.
- `waitForManagedAPI`, `observeListenerGap`, `assertSharedEndpoints`: only
  `standby`.

## Template loading change

Today the harness does
`template.ParseFiles(filepath.Join("testdata", "project.hcl.tmpl"))`, which
resolves relative to the working directory. When the harness becomes a package
imported from `cmd`, and later when scenarios are categorized, relying on the
working directory is fragile.

Move the template to `scenarios/internal/harness/testdata/project.hcl.tmpl` and
embed it:

```go
//go:embed testdata/project.hcl.tmpl
var projectTemplate string
```

Render with `template.New("project").Parse(projectTemplate)`. This removes the
working-directory dependency for the template entirely.

## Builder bootstrap, handled temporarily

`runMain`/`TestMain` in `harness_test.go` compiles the builder CLI once into a
temp binary and stores the path in the package var `builderBinary`, plus it
short-circuits `-short`. That logic does not belong in the harness package long
term; it moves into the command in stage 03.

For this stage, keep it working with the least churn:

- Keep a `TestMain(m *testing.M)` in the scenarios test package (`package
  scenarios`, in a file such as `main_test.go`) that does the builder compile
  and calls `harness.SetBuilder(path)` before `m.Run()`.
- Add `harness.SetBuilder(path string)` and an internal `builderBinary` var in
  the harness package, read by `DeploySite`/`buildProject`. This is the same
  singleton shape the code already has, just addressable from outside.
- The builder directory lookup (`filepath.Abs(filepath.Join("..", "builder"))`)
  stays working-directory relative for now; stage 03 makes it a command
  responsibility with an override flag.

Stage 03 replaces this `TestMain` bootstrap with the command doing the same work
before `MainStart`.

## Steps

1. Create the `internal/harness` package and move `harness_test.go` and
   `registrationapi_test.go` content into non-test `.go` files there, renaming
   every cross-package identifier per the tables above.
2. Move the fabric-config helpers out of `warm_standby_test.go` into
   `harness/fabric.go` and export them.
3. Move and embed the template; delete `scenarios/testdata/` once nothing reads
   it from disk.
4. Add `harness.SetBuilder` and keep the builder var internal.
5. Rewrite the scenario `*_test.go` files to import `internal/harness` and call
   the exported API. Replace every bare `deploySite(...)`, `propose(...)`,
   `node.binaryPath`, etc. with the `harness.` qualified form.
6. Add a `main_test.go` in `package scenarios` holding the `TestMain` bootstrap.
7. Delete the now-empty original `harness_test.go`, `registrationapi_test.go`,
   and the old package `doc.go` content that described the harness (fold the
   still-relevant prose into `harness/doc.go`; keep a short root `doc.go` if the
   `scenarios` package still has test files).

## Files touched

- New: `scenarios/internal/harness/*.go`,
  `scenarios/internal/harness/testdata/project.hcl.tmpl`.
- Rewritten to call the harness: all eight scenario `*_test.go` files.
- New: `scenarios/main_test.go` (temporary bootstrap).
- Deleted: `scenarios/harness_test.go`, `scenarios/registrationapi_test.go`,
  `scenarios/testdata/`.

## Verification

- `go build ./...` and `go vet ./...` in `scenarios/` succeed.
- `go test -run x -count=1 ./...` in `scenarios/` compiles every test file
  (the `-run x` matches nothing, so it only checks compilation quickly).
- `task fast` passes.
- Optionally run one scenario end to end with `go test -run
  TestBuildAndRunMinimumSite -count=1` to confirm the embedded template and
  `SetBuilder` path work. Scenario success is not required by this plan, but a
  clean build failure versus a scenario assertion failure must be
  distinguishable.

## Risks and notes

- Highest-churn stage. Expect many compile errors mid-edit; work package-inward
  (get `internal/harness` compiling on its own first, then fix the scenario
  files).
- Method-name shadowing: `Machine` embeds `*procrun.Process`, which already has
  `Running`, `Logs`, `Wait`, `Kill`, `Stop`, `PID`. The current unexported
  wrappers `running`, `logs`, `wait` differ only by case. When exporting, pick
  distinct names (for example `IsRunning`, `Output`, `AwaitExit`) so the wrapper
  does not silently collide with or shadow the embedded method. Update scenario
  call sites accordingly.
- Field mutation across packages: `manifest_contract_test.go` sets
  `node.launchArgs = ...` and `node.Process = nil` directly. With exported
  fields this keeps working, but consider a small method such as
  `Machine.Relaunch(args []string)` to avoid exposing raw relaunch mechanics.
  Either is acceptable; the method is cleaner.
- Keep `utils/testnet` imported as before. Do not pull it into the harness
  package's import rewrites.
