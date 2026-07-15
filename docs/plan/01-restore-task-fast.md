# Stage 1: Restore `task fast`

**Estimate:** 0.5 day.

## Goal

Make the fast developer gate run reliably from a clean checkout before changing
fabric or registration behavior.

## Current failure

Two baseline failures were observed:

- One `task fast` run stopped in `go mod tidy` for the `scenarios` module because
  Go used `C:\Users\Miroslav.Matejovsky\AppData\Local\go-build`, which was not
  writable in the managed workspace.
- A later `task all` run reached lint and failed because
  `scenarios/harness_test.go` ignores both values returned by `p.wait()` in test
  cleanup.

## Work

1. Configure a repository-local Go build cache for every task, including scripts
   that invoke Go directly. Use the existing ignored `.gocache/` directory.
2. Configure golangci-lint to use the existing ignored `.lintcache/` directory,
   so the next fast-gate step does not fail for the same reason.
3. Keep the module download cache unchanged. Moving it would force unnecessary
   dependency downloads and is not required by the observed failure.
4. Decide whether `clean` should remove tool caches. Prefer retaining them so a
   fast gate remains fast; document the choice in `taskfile/README.md`.
5. Add a small task-script check that fails with a clear path-specific message
   if a configured cache cannot be created.
6. Fix the scenario cleanup lint error. Cleanup kills the process deliberately,
   so explicitly discard the resulting output and exit error and document why
   they are not assertions at that point.

## Tests

- Run `task fast` from the repository root.
- Run `task fast` a second time to cover reuse of the local caches.
- Confirm no tracked or untracked cache content appears in `git status`.

## Exit criteria

- `task fast` passes twice.
- All Go and lint caches used by the fast gate are writable workspace-local
  paths.
- The task documentation states where the caches live and what `clean` removes.

## Implementation result

Completed on 2026-07-15. `task fast` passes twice. The second run reuses the
workspace-local caches. The gate reports 254 passing tests with only the
expected short-mode skips.
