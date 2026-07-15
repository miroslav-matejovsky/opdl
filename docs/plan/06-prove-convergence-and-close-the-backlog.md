# Stage 6: Prove convergence and close the backlog

**Estimate:** 1 to 2 days.

## Goal

Prove the relaxed guarantee against two real Olric members and make the final
architecture documentation match the implemented behavior.

## Scenario

1. Start node A alone and accept a registration fully.
2. Start node B so Olric moves partitions while the site already holds data.
3. Submit a different proposal for the same unit key through node B during the
   known join window. Use a focused test hook or repeated bounded attempts only
   if needed to make the false-win path deterministic; do not use sleeps as the
   assertion mechanism.
4. Allow the second proposal to be visible temporarily if the backend admits it.
5. Poll observable API state until reconciliation restores node A as winner and
   marks node B's proposal rejected.
6. Assert `GET /registrations` exposes both proposals and
   `GET /registrations/conflicts` reports the resolved conflict.
7. Restart a reconciler or platform member and assert the result remains the
   same.

The scenario must also retain the ordinary behavior: a conflict that is already
visible at POST time returns `409` immediately and never displaces the winner.

## Documentation cleanup

1. Update the root `README.md` end-to-end registration example and conflict
   section.
2. Update package documentation for fabric, registration, HTTP API, scenarios,
   and events where their promises changed.
3. Change `docs/backlog/fabric.md` from an unresolved defect into a short record
   of the limitation and the implemented mitigation, or remove the indexed item
   and retain the rationale in nearby architecture documentation.
4. Update `docs/backlog/README.md` and `.todo` so neither claims the strict defect
   or task-run failure remains open.
5. Record active notifications, acknowledgement, retention, and removal as
   separate backlog work only if they are still wanted.

## Final validation

- Run the focused two-member Olric test repeatedly.
- Run the registration and HTTP API unit tests with `-race`.
- Run the generated SDK tests.
- Run `task fast`.
- Run `task all`.

## Exit criteria

- A conflicting proposal can be transiently visible but converges to rejected.
- The earlier accepted service remains registered in the late-join case.
- The inconsistency and its resolution are visible through the registration API.
- Documentation states an eventual, best-effort-first guarantee and its clock
  limitation, not global linearizability.
- `task all` passes.

