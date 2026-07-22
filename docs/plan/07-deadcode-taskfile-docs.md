# Stage 07: deadcode script, docs, and final gate reconciliation

Effort: S-M (about 0.5 to 1 day). Complexity: Low.

## Goal

Finish the refactor: stop treating `scenarios` as a special case in
`deadcode.ps1`, update the repo documentation to describe the new structure, and
confirm the full gate is consistent. No scenario behavior changes here.

## deadcode.ps1

Today the script special-cases `scenarios` because it had no `cmd/` directory:

```powershell
$roots = @($CmdModules + "scenarios") | Sort-Object -Unique
```

After stage 04, `scenarios` has `cmd/`, so `modules.ps1` already discovers it as
a command module through its `$CmdModules` probe. Remove the special case:

```powershell
$roots = @($CmdModules) | Sort-Object -Unique
```

Then update the surrounding comments that say "command modules and scenarios" to
just "command modules", since scenarios is now one of them. The instruction for
this refactor was explicit: do not treat scenarios separately, the `cmd` folder
is enough.

Confirm both deadcode layers still hold:
- Layer 1 (unreachable functions): the scenario functions are reachable from
  `cmd/main.go` through each package's `Scenarios()`. The `-filter` on the
  module prefix is unchanged.
- Layer 2 (orphan packages): `internal/harness`, `internal/runner`, and the six
  category packages are all reachable from `cmd`. The moved `internal/procrun`
  and friends are reachable through the harness. `builder/build` is reachable
  from both `builder/cmd` and the scenarios harness. Nothing is orphaned.

## Documentation

- `scenarios/internal/harness/doc.go`: the harness, concurrency budget, artifact
  layout, and evidence prose from the old root `doc.go` live here (moved in
  stage 03). Verify it is current: it should describe `internal/procrun`,
  `internal/semaphore`, `internal/waitfor`, `internal/logscan`, note that
  `testnet` still comes from `utils`, and that the build now runs in-process
  through `builder/build` rather than by driving the builder CLI.
- `scenarios/cmd/doc.go` (or the `main` package doc): describe the command, its
  flags (`-parallel`, `-timeout`, `-run`, `-count`, `-only`, `-skip`, `-list`,
  `-platform-dir`), and that it runs scenarios through the standard test
  runner via `testing.MainStart`.
- Root `README.md`: update any description of how scenarios run (from `go test`
  to `go run ./cmd`) and the module layout.
- `AGENTS.md`: no rule changes needed, but if it references the scenarios layout
  anywhere, align it.
- Consider deleting `docs/plan/` once the refactor lands, or keep it as a record.
  That is the requester's call.

## Optional: architecture spec for scenarios

`platform` ships a `.go-arch-lint.yml` and the `arch` task runs it per module.
Scenarios now has a real internal structure worth constraining. Optionally add
`scenarios/.go-arch-lint.yml` to encode the intended dependency direction:

- `cmd` may import `internal/runner` and the six category packages.
- category packages may import `internal/harness` and `internal/runner`.
- `internal/harness` may import the moved `internal/*` helpers, `utils/testnet`,
  and `builder/build` (plus `builder/deployment` if it uses those types).
- category packages must not import each other.
- nothing outside `cmd` may import a category package.

This is optional. If added, the `arch` task picks it up automatically because
`arch.ps1` runs on every module that has the spec.

## Final gate reconciliation

Run and confirm:

- `task tidy`: `scenarios/go.mod` requires `utils` (for `testnet`) and `builder`
  (for `builder/build`), with `replace` directives to `../utils` and
  `../builder`, and nothing stale remains. The moved utils packages add no
  external module beyond `testify`; `builder` brings its own dependency tree
  (HCL and friends) into the scenarios build, which is expected.
- `task vet`, `task fmt`, `task lint`: clean across all modules.
- `task deadcode`: clean, with the special case removed.
- `task arch`: clean (and enforcing the new spec if added).
- `task test`: the relocated `_test.go` unit tests (procrun, testnet callers,
  etc.) run under the scenarios module with `-short` and pass. This is the unit
  half; it must stay green.
- `task fast`: green end to end.
- `task scenarios`: runs `go run ./cmd`. It is still not expected to pass its
  scenarios, per the refactor's stated scope. Confirm it reaches scenario logic
  and fails on assertions or environment, not on wiring, imports, or missing
  files.

## Files touched

- Edited: `taskfile/deadcode.ps1`.
- Edited: root `README.md`, and `AGENTS.md` if it references the layout.
- New (optional): `scenarios/.go-arch-lint.yml`.
- Verified: `scenarios/cmd/doc.go`, `scenarios/internal/harness/doc.go`.

## Verification

Covered by the final gate reconciliation above. The single acceptance line: on a
clean tree, `task fast` is green, `task test` is green, `task deadcode` no longer
names scenarios specially, and `task scenarios` runs the command and fails only
inside scenario logic.

## Risks and notes

- Low risk. Script and docs only.
- If `.go-arch-lint.yml` is added and the real import graph disagrees with the
  intended one, fix the graph, not the spec, unless the intent was wrong.
- Do not re-enable `# - task scenarios` inside `task all`. It stays commented;
  scenarios are not part of `task all` for now, by decision.
