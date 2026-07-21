# Phase 8: Windows-only consolidation

Remove Linux and Unix-specific code. The product targets Windows; the alternate
code paths are unreachable, untested, and pure cognitive load.

## Why this is justified rather than merely tidy

The repository already behaves as a Windows-only project everywhere except the
source.

- **There is no CI.** No `.github`, no pipeline configuration of any kind. The
  "Linux CI" that `docs/backlog/redundancy.md:29` and
  `docs/operations/monitoring.md:93` refer to does not exist and never has.
- **All tooling is PowerShell.** Every file in `taskfile/` is a `.ps1`. There is no
  shell equivalent, so `task all` cannot run on Linux at all.
- **The Unix code paths are therefore never executed.** Not in CI, because there
  is none. Not by `task all`, because it cannot run there. They compile and
  nothing more.

Keeping a second implementation that is never run is worse than having none: it
implies a portability guarantee that nothing verifies, and a reader has to
consider both paths when reasoning about behavior.

Two documentation statements should be corrected regardless of whether the code
changes, because they promise validation that cannot happen:
`docs/backlog/redundancy.md:29` makes Linux CI a precondition for stating an SLO,
and `docs/operations/monitoring.md:93` says the same for percentiles. Those
preconditions are currently unsatisfiable.

## Placement

After lock-file retirement. Retiring the file lock deletes `utils/filelock`
entirely, including `lock_unix.go`, so the largest single item is removed for free.
Doing this phase earlier means editing a file that is about to be deleted.

## Inventory

Complete, from a repository-wide search for build tags and `GOOS` branching.

### Removed with the file lock, Epic 6

| Path | Note |
| --- | --- |
| `utils/filelock/lock_unix.go` | `//go:build !windows`, `flock` |
| `utils/filelock/lock_windows.go` | goes too; the whole package is deleted |

### Build-tagged files to remove

| Path | Tag | Action |
| --- | --- | --- |
| `utils/atomicfile/replace_unix.go` | `!windows` | Delete; fold `replace_windows.go` into the package |
| `utils/processinfo/resident_linux.go` | `linux` | Delete |
| `utils/processinfo/resident_other.go` | `!linux && !windows` | Delete |
| `utils/processinfo/processinfo_linux_test.go` | `linux` | Delete |
| `utils/processinfo/processinfo_other_test.go` | `!linux && !windows` | Delete |
| `utils/processtree/owner_unix.go` | `!windows` | Delete |

After deletion the remaining `_windows.go` files no longer need their build tags,
since the package targets one platform. Removing the tag is what actually reduces
the cognitive load: a reader stops having to ask which variant is in play.

`utils/processinfo` and `utils/processtree` are used only by the scenario harness
(`scenarios/harness_test.go`, `scenarios/warm_standby_test.go`), so the blast
radius is test-only.

### GOOS branching to remove

| Path | Line | Branch | Action |
| --- | --- | --- | --- |
| `scenarios/harness_test.go` | 75 | `builderBinary` drops `.exe` when not Windows | Always `.exe` |
| `scenarios/harness_test.go` | 279 | `machineBinary` adds `.exe` when Windows | Always `.exe` |
| `utils/atomicfile/write_test.go` | 45 | Windows-conditional assertion | Assert unconditionally |
| `builder/internal/pack/pack.go` | 205 | `effectiveGOOS` falls back to `runtime.GOOS` | Pin `windows` |
| `builder/internal/pack/pack.go` | 197 | `GOOS=` in the build environment | Keep, pinned |
| `builder/internal/pack/pack.go` | 221 | `.exe` suffix when Windows | Unconditional |

`builder/internal/pack` needs the most care. It cross-compiles packages, so the
`GOOS` plumbing is deliberate rather than accidental. The recommendation is to pin
the target rather than delete the mechanism: the builder still sets `GOOS=windows`
explicitly for reproducibility, but stops branching on the host. That keeps builds
deterministic on any host while removing the question of what happens on a
non-Windows one.

### Dependency

`golang.org/x/sys/unix` enters through `lock_unix.go`, `replace_unix.go`,
`resident_linux.go`, and `owner_unix.go`. After all four are gone, `task tidy`
should drop it. `golang.org/x/sys/windows` stays.

### Documentation

| Path | Line | Change |
| --- | --- | --- |
| `docs/backlog/redundancy.md` | 29 | Remove Linux CI as an SLO precondition; the precondition is unsatisfiable |
| `docs/operations/monitoring.md` | 93 | Same for percentiles |
| `utils/filelock/doc.go` | 4, 10 | Deleted with the package |

`docs/plans/leadership/` also recommends a portable notification channel to
preserve Linux coverage. That recommendation is void under a Windows-only
decision, and the named-pipe stage there simplifies accordingly. The two plans
should be reconciled before either is implemented.

## Risks

- **Low technical risk, and it is genuinely low.** Every removed path is currently
  unexecuted. Nothing that runs today stops running.
- **One-way door.** Restoring Linux support later means rewriting these
  implementations. This is a product decision, and the cost of reversal should be
  accepted explicitly rather than discovered.
- **`builder/internal/pack` is the only non-trivial edit.** Over-aggressive
  simplification could remove reproducibility that the `GOOS` setting provides.
  Pin rather than delete.
- **Scope creep.** This phase is deletion. It should not become a refactor of
  `atomicfile` or `processinfo` while their files are open.

## Validation

- `task all` passes.
- `task deadcode` reports nothing orphaned by the deletions.
- `task tidy` produces no `golang.org/x/sys/unix` in any module.
- A repository search for `go:build`, `GOOS`, and `runtime.GOOS` returns only the
  pinned builder target.
- Scenarios still build and run real packages.

## Rollback

Revert. The change is deletions plus two small edits, with no runtime behavior
change on Windows.

## Definition of done

- No `!windows`, `linux`, or `unix` build-tagged file remains.
- Remaining Windows implementations carry no build tag.
- No `runtime.GOOS` branch remains outside the pinned builder target.
- `golang.org/x/sys/unix` is absent from the dependency graph.
- No documentation promises Linux CI or Linux validation.
- `task all` passes.
