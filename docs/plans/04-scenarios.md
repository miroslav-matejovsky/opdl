# Step 04 — Redundancy scenarios

Complexity: **Medium** · Effort: **M (~1 day)** · Depends on: step 02

## Why

Integration tests (step 03) prove the ownership machine in-process. Scenarios
prove the real thing: two OS processes built from a real blueprint, coordinating
over a real lease file, observed only through their public surfaces (the HTTP
API and the local JSONL event record). The failback scenario in particular is
only possible after step 02 — without in-place transitions the Standby exits on
failback and there is no service manager in the harness to bring it back.

## Design

A new `scenarios/redundancy` package, registered like `smoke` is. It uses the
existing harness: a two-instance single-machine fixture (standby enabled), the
rendered `lease` block with short timings suitable for a test (e.g. duration
2s, renewal 500ms, health check 250ms, stabilization 2s — rendered into the
template rather than the production 15s/5s/2s/30s), `StartManaged` per role,
and the platform API client for polling `/health` and `/health/ha`.

Remember the scenario cache: scenarios build the platform binary at run time,
so after editing `platform/**` run them with `-count=1`.

## Scenario 1 — `FailoverAndFailback` (the core scenario)

One scenario, the whole story, because the second half needs the first half's
state anyway:

1. Start Primary and Standby. Wait until Primary reports
   `runtimeState: active` and Standby `passive` on their own API addresses.
   Assert the Standby, while Passive, answers `GET /health` (200) and refuses a
   domain operation (503) — the step-01 contract observed end-to-end.
2. **Failover:** kill the Primary process (hard stop, no graceful release).
   Wait ≤ lease duration + margin for the Standby to report `active` and
   `leaseState: Owned` on `/health/ha`.
3. **Failback:** start the Primary again. It must come up Passive (Standby owns
   a valid lease). After the stabilization window, the Primary reports
   `active` and the Standby reports `passive` — and the Standby answers that
   from the **same process** (assert its PID never changed).
4. Throughout, a poller samples both `/health` endpoints every ~100ms and fails
   the run if both ever report `active` — the split-brain assertion at the
   process level.
5. On failure, dump `diagnostics(machines...)` (lazily, so the dump reflects
   the moment of failure) including both instances' `events.jsonl`.

## Scenario 2 — `GracefulHandover` (small, optional if time is short)

1. Start both; Primary Active.
2. Stop the Primary **gracefully** (interrupt). Its release is written, so the
   Standby must promote fast — well under the lease duration — proving the
   released-lease fast path that a hard kill cannot show.
3. Assert the takeover event is `ownership_acquired{abandoned:false}` in the
   Standby's event record (a clean handover, not a crash takeover).

## Tasks

1. Add the `redundancy` scenario category and register it with the runner.
2. Extend the harness fixture only if needed for per-scenario lease timings
   (the template already renders the lease block; timings may need to become
   fixture fields instead of literals).
3. Implement scenario 1; implement scenario 2 if it fits the budget.
4. Wire into the scenarios task so CI runs them.

## Done when

- Scenario 1 passes repeatedly (`-count=1` each run by design) on a developer
  machine.
- A deliberately broken failback (e.g. stabilization never satisfied) fails the
  scenario with a diagnostic that names both instances' last health states.
