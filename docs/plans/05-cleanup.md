# Step 05 — Alignment and close-out

Complexity: **Low** · Effort: **S (~½ day)** · Depends on: steps 01–04

## Why

The redundancy work has moved fast across several passes (mutex → lease →
failback → in-place transitions). Comments, package docs, and the platform
README each describe the state at the time they were written. One pass at the
end makes the code the single story, so the plan documents can be deleted
instead of maintained.

## Tasks

1. **Package docs.** `platform/internal/redundancy/doc.go` and
   `platform/internal/httpapi` doc comments describe the final model: lease
   loop, in-place transitions, explicit serving modes, automatic-only policy.
   Remove any remaining "the service manager restarts it" phrasing and stale
   references to removed concepts (mutex, fencing, manual failback).
2. **`platform/README.md`.** The redundancy paragraphs describe the lease and
   the Passive health surface; verify they match, fix where they do not.
3. **Event catalog audit.** Confirm every `platform.redundancy.*` event is
   still stated somewhere and reads correctly for the loop model; drop any that
   became unreachable.
4. **Config summary.** Startup summary lines (`lease …`) still render every
   authored value; nothing prints removed settings.
5. **Plans close-out.** When 01–04 are merged and green, delete
   `docs/plans/01…05` and this file, leaving `docs/plans` empty (or a one-line
   README pointing at the code). The docs index (`docs/README.md`) keeps its
   Plans entry only if the folder keeps a README.
6. Full sweep: all module tests, `-race` on redundancy, arch-lint,
   `task deadcode`, and one `-count=1` scenario run.

## Done when

- A newcomer can understand the redundancy design from `doc.go` + the platform
  README alone, with no plan document required.
- Every checker in the repo is green in one run.
