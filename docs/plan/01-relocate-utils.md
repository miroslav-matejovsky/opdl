# Stage 01: relocate scenario-only utils packages into scenarios/internal

Effort: S (about 0.5 day). Complexity: Low.

## Goal

Move the six utils packages that only scenarios use into
`scenarios/internal/`, and repoint the scenario imports at the new locations.
Keep `utils/testnet` where it is; scenarios still import it. Leave the four
platform-only utils packages untouched.

After this stage the scenarios still build and run exactly as before, through
`go test`. Nothing about the runner or categorization has changed yet. This
stage exists on its own because it is independent, low risk, and verifiable in
isolation.

## Preconditions

- Clean working tree.
- `task fast` passes before starting, so any breakage is attributable.

## Packages that move

| From                    | To                                   | Used by scenarios in            |
| ----------------------- | ------------------------------------ | ------------------------------- |
| `utils/procrun`         | `scenarios/internal/procrun`         | harness, dotnet sdk             |
| `utils/processtree`     | `scenarios/internal/processtree`     | dependency of `procrun`         |
| `utils/semaphore`       | `scenarios/internal/semaphore`       | harness (budget, build budget)  |
| `utils/waitfor`         | `scenarios/internal/waitfor`         | harness (Poll, Abort)           |
| `utils/logscan`         | `scenarios/internal/logscan`         | eventfabric, warm standby       |
| `utils/processinfo`     | `scenarios/internal/processinfo`     | warm standby (ResidentBytes)    |

`procrun` imports `processtree`, so both move together. None of the six import
any other utils package, and none import an external module beyond
`github.com/stretchr/testify/require` and the standard library, which the
scenarios module already requires. So no new module dependency is introduced.

## Packages that stay in utils

- `testnet`: shared with `platform` (`app_test.go`, `nats/nats_test.go`).
  Scenarios keep importing `github.com/miroslav-matejovsky/opdl/utils/testnet`.
- `atomicfile`, `jsonfields`, `stablehash`, `winmutex`: platform and builder
  only, not used by scenarios.

## Steps

1. Create `scenarios/internal/`.

2. For each of the six packages, move the whole directory (all `.go` files
   including `doc.go` and `_test.go`) from `utils/<pkg>` to
   `scenarios/internal/<pkg>`. The Go package name inside the files does not
   change (still `package procrun`, etc.); only the import path changes.

3. Update the import path inside `procrun` for its `processtree` dependency,
   from `github.com/miroslav-matejovsky/opdl/utils/processtree` to
   `github.com/miroslav-matejovsky/opdl/scenarios/internal/processtree`.

4. Update the scenario files that import these packages. The import base path
   changes from `.../opdl/utils/<pkg>` to `.../opdl/scenarios/internal/<pkg>`:
   - `harness_test.go`: `procrun`, `semaphore`, `waitfor`.
   - `dotnet_sdk_e2e_test.go`: `procrun`.
   - `two_machine_eventfabric_test.go`: `logscan`.
   - `warm_standby_test.go`: `logscan`, `processinfo`.
   `harness_test.go` keeps `.../opdl/utils/testnet` unchanged.

5. Remove the six moved directories from `utils/`. Confirm nothing else in the
   repo imports them (they were scenario-only, so this is a no-op elsewhere; the
   grep in verification proves it).

6. Run `task tidy`. This rewrites `utils/go.mod`, `utils/go.sum`,
   `scenarios/go.mod`, and `scenarios/go.sum`. The utils module loses whatever
   direct requirement the moved packages introduced (none beyond testify).
   `scenarios/go.mod` keeps its `require utils` and its `replace utils => ../utils`
   because `testnet` still lives there.

## Files touched

- Moved: `utils/{procrun,processtree,semaphore,waitfor,logscan,processinfo}/**`
  to `scenarios/internal/...`.
- Edited imports: `scenarios/harness_test.go`,
  `scenarios/dotnet_sdk_e2e_test.go`,
  `scenarios/two_machine_eventfabric_test.go`,
  `scenarios/warm_standby_test.go`, plus `procrun`'s internal reference to
  `processtree`.
- Regenerated: the two `go.mod`/`go.sum` pairs.

## Verification

- `grep -r "opdl/utils/\(procrun\|processtree\|semaphore\|waitfor\|logscan\|processinfo\)"`
  returns nothing across the repo.
- In `scenarios/`: `go build ./...` and `go vet ./...` succeed.
- In `utils/`: `go build ./...` and `go vet ./...` succeed.
- `task test` runs the relocated `_test.go` files under the scenarios module
  now, still with `-short`. Confirm they pass in their new home. They already
  ran under `-short` in the utils module, so behavior should be identical; the
  point of checking is only that the relocation did not break a `-short` guard.
- `task fast` passes (this exercises `deadcode`, `lint`, `arch`, `vet`, `test`).
  `deadcode.ps1` still special-cases `scenarios`; that is fine and is cleaned up
  in stage 07.

## Risks and notes

- Low risk. The change is a directory move plus import-path edits.
- `deadcode` layer 2 flags packages not reachable from a command or scenario
  module. The moved packages are reachable through the scenario `_test.go`
  files via `go list -test`, so they are not reported as orphans.
- Do not move `testnet`. Moving it would break `platform` tests, which is out of
  scope and was explicitly decided against.
