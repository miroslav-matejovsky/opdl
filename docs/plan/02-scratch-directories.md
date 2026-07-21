# Stage 2: repo-local scratch directories

## Goal

Move every scenario artifact from the system temporary directory into
`scenarios/.tmp/<TestName>/`, replaced on each run. A developer investigating a
failure finds the rendered blueprint, the built binaries, the journal, the
status files, and the operational event streams next to the code, not
somewhere under `AppData\Local\Temp`.

## Why

Scenarios produce exactly the evidence needed to understand a failure, and
then hide it. Four calls to `t.TempDir()` place it under a per-run path with a
generated suffix:

- `outDir` and `workDir` in every scenario, for example
  `scenarios/build_and_run_test.go:18`.
- the blueprint root inside `stageBlueprint`
  (`scenarios/harness_test.go:169`).
- `controlDir` for the marker handshake
  (`scenarios/dotnet_sdk_e2e_test.go:56`).

Go removes those directories when the test ends unless the run failed, and
even on failure the path is buried and differs every run. So the normal
investigation loop is: read a failure message, decide it is not enough, go
find the directory, discover it is gone or that there are six of them from
earlier runs, and re-run with a hand-edited harness that keeps the files.

A fixed repo-local path removes all of that. `scenarios/.tmp/TestXxx/work/`
is the same path every time, it is a short relative path from the module
directory, and it holds the last run's evidence until the next run replaces
it.

The evidence is substantial and is already structured for reading: the journal
per machine in `nats-<name>/`, the process status files in
`instance-<name>/`, and the operational JSONL streams in
`operations-<name>/` (`scenarios/harness_test.go:320`). `operationEvents`
(`harness_test.go:660`) exists purely to paste that last directory into a
failure message. With a stable path, a developer can open the files directly
instead.

## Design

One helper in the harness, replacing every `t.TempDir()` call.

```go
// scenarioDir returns this scenario's scratch root under scenarios/.tmp,
// emptied on first use in this run.
func scenarioDir(t *testing.T) string
```

Layout:

```
scenarios/.tmp/
  TestFourMachineStorageTopologyAndFailure/
    blueprints/    rendered project.hcl
    out/           built machine packages and manifests
    work/          config-*.toml, nats-*, instance-*, operations-*
    control/       marker files
```

Decisions:

- **Keyed by top-level test name, not `t.Name()`.** `t.Name()` inside a
  subtest yields `TestManifestArgumentsMatchRuntime/primary`, which would
  split one deployment's artifacts across two directories and can contain
  characters that are illegal in a Windows path. Cut at the first `/`. The
  subtests in `scenarios/manifest_contract_test.go:29` share one machine
  anyway, so one directory is the correct model.
- **Emptied once per run, at first use.** `os.RemoveAll` then `os.MkdirAll`,
  guarded by a `sync.Once` per test name so a scenario that calls it twice
  does not wipe its own first half. No timestamps, no run identifiers,
  no accumulation.
- **Never removed at the end.** The whole point is that the files outlive the
  test. Nothing registers a cleanup.
- **Short subdirectory names.** `out` and `work`, not descriptive ones.
  Windows still has a 260 character path limit by default, and NATS JetStream
  writes deep paths under the data directory. The repo-local root is shorter
  than the system temp root on a typical Windows profile, so this is a small
  improvement over today, but only if the names below it stay short.
- **The leading dot matters.** The Go tool ignores directories beginning with
  `.` or `_` when matching `./...`, so built binaries and rendered `.hcl`
  under `.tmp` are invisible to `go build`, `go vet`, `golangci-lint`, and
  `deadcode`. A directory named `tmp` would not be.
- **An environment override.** `OPDL_SCENARIO_TMP` relocates the root. This is
  not speculative generality: stage 1's verification runs two copies of the
  scenario package at once to exercise the port pool across processes, and a
  fixed path makes that impossible without it. See the conflict below.

## The conflict this creates, stated plainly

A fixed path and concurrent test processes are incompatible. Two `go test`
runs of the same scenario will empty each other's directory mid-run.

This is not a problem for the parallelism in stage 4, which runs scenarios
concurrently inside one process, each with its own directory. It is a problem
for two separate `go test` invocations, which is a thing developers do and
which stage 1 recommends as a verification step.

The resolution is `OPDL_SCENARIO_TMP`: the second process sets it and gets its
own root. Stage 1's verification is amended accordingly. Do not solve this
with timestamps or PIDs in the default path. That would restore the exact
problem this stage exists to remove, which is that the interesting directory
is never the one you are looking at.

## Changes

`scenarios/harness_test.go`
- Add `scenarioDir` and its per-test-name `sync.Once` map.
- `stageBlueprint` (`:169`) uses `scenarioDir(t)/blueprints`.
- Document the layout in the function comment, since that comment is what a
  developer will find when they go looking for where the files went.

`scenarios/*_test.go`
- Replace `t.TempDir()` at every call site with the corresponding
  `scenarioDir(t)` subdirectory. Nine scenarios, roughly seventeen calls.

`.gitignore`
- Add `.tmp/` next to the existing `.gocache/`, `.lintcache/`,
  `.test-results/`, and `.data/` entries. Same category, same reasoning.
- Note that `*.exe` is already ignored globally, so the built machine binaries
  would be ignored regardless. The directory entry is still needed for the
  journals, configs, manifests, and rendered blueprints.

`taskfile/clean.ps1`
- Add `.tmp` to `$foldersToRemove` (`clean.ps1:4`). It is applied at the repo
  root and in each module directory, so `scenarios/.tmp` is covered.
- This matters more than it looks: `task all` runs `task clean` first, and
  clean already deletes every `*.exe` recursively (`clean.ps1:21`). Without a
  `.tmp` entry, a clean would strip the binaries out of the scratch tree and
  leave the journals behind, which is the worst of both.

`scenarios/doc.go`
- Document where artifacts live and that they survive the run. This is the
  first thing someone debugging a scenario needs to know and there is
  currently no reason for them to guess it.

## Risks

- **Disk growth.** Fifteen platform binaries plus JetStream journals, retained
  after every run. Expect a few hundred megabytes sitting in the working tree.
  `task clean` removes it, and the tree is replaced rather than appended to,
  so it does not grow without bound. Worth stating the expected size in
  `doc.go` so nobody is surprised.
- **Locked files on Windows.** `os.RemoveAll` fails with an access error if a
  leaked process from a previous run still holds a file. `processtree` puts
  every child in a kill-on-close job so this should not happen, but a hard
  timeout can still leak. Fail with a message that names the path and says a
  stale process may be holding it. A bare `Access is denied` on a path the
  developer did not know existed is a bad first experience of this change.
- **Clean walks a larger tree.** `clean.ps1` scans recursively for `*.exe`.
  Removing `.tmp` as a folder first makes that scan cheaper, not more
  expensive, so ordering within the script is worth a glance.
- **Antivirus.** Repo directories are often scanned more aggressively than the
  system temp directory. If scenario runtime regresses noticeably on a
  developer machine, this is the first thing to check.

## Verification

- Run one scenario. Confirm `scenarios/.tmp/<TestName>/` exists afterwards and
  contains the rendered `project.hcl`, the built binary and `manifest.json`,
  the journal, the status files, and the operational JSONL.
- Run it again. Confirm the directory was replaced, not added to, and that no
  second directory appeared.
- Run the full suite. Confirm one directory per scenario and no leftovers from
  scenarios that were not run.
- `git status` is clean after a run.
- `go build ./...`, `task vet`, `task lint`, and `task deadcode` in the
  `scenarios` module all ignore `.tmp`. This is the check that the leading dot
  is doing its job.
- `task clean` removes it.
- Force a failure and confirm the failure message and the on-disk evidence
  agree.

## Done when

- No `t.TempDir()` call remains in the `scenarios` module.
- Artifacts are at a predictable path and survive the run.
- `.gitignore` and `clean.ps1` both know about `.tmp`.
- `OPDL_SCENARIO_TMP` is documented in `scenarios/doc.go` alongside the reason
  it exists.
</content>
