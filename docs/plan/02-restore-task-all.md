# Stage 2: Fix and verify the full gate

**Estimate:** 0.5 to 1 day.

## Goal

Fix every remaining full-gate failure after `task fast` is green. Do not begin
the fabric change until the complete baseline passes.

`task all` currently stops at the same scenario lint error assigned to Stage 1,
before it reaches any full-only task. Stage 1 fixes that actual code issue because
lint is shared by both gates. This stage then exposes and fixes failures in the
full-only tasks rather than assuming that the shared fix is sufficient.

## Work

1. Run each full-only task directly in gate order: `task sdk-dotnet`,
   `task validate`, and `task scenarios`. This produces a concrete failure for
   each repair without repeatedly paying for the already-green fast gate.
2. Fix every task infrastructure, production code, or test defect that prevents
   those commands from passing. A failing product test is not dismissed as an
   environment problem, and a test is not weakened to make the gate green.
3. Add or adjust regression tests when a production defect is found. Keep a
   task-only repair covered by the smallest practical script check.
4. Keep environment fixes in shared task setup when several scripts need them.
   Do not duplicate cache setup in each script.
5. Run `task all`. If it exposes an ordering or cleanup failure that the direct
   subtasks did not, fix that failure and rerun the affected subtask before
   rerunning the complete gate.
6. Remove the obsolete task-run limitation from `.todo` only after the complete
   gate passes.

## Tests

- Run every full-only subtask directly and require each to pass.
- Run `task fast` after task infrastructure or shared code changes.
- Run `task all` from the repository root after all repairs.

## Exit criteria

- `task fast` still passes.
- `task sdk-dotnet`, `task validate`, and `task scenarios` each pass directly.
- `task all` passes without manual environment setup.
- Every observed failure has a repair or regression test. None is merely noted
  and carried into the fabric stages.

## Implementation result

Completed on 2026-07-15. `task all` passes after the Stage 1 task fixes. No
additional tests were marked ignored. The output contains only the existing
short-mode and environment-dependent SDK skips; full platform integration and
scenario tests pass.
