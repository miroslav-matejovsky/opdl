# Stage 4: Retain and reconcile registration contenders

**Estimate:** 2 to 3 days.

## Goal

Make registration converge after a false create win without locks, quorum,
leader election, or a new storage backend.

## Storage model

1. Replace the single destructive request history with immutable contender
   records keyed by unit key and proposal fingerprint.
2. Add a platform-observed UTC request time to each contender. Inject the clock
   into the service for deterministic tests.
3. Retain an immutable acceptance marker per contender instead of treating the
   mutable registration projection as the only evidence that acceptance
   happened.
4. Keep confirmations scoped by proposal fingerprint. Their existing key shape
   already separates competing proposals.
5. Treat the single request and accepted records, if retained as projections,
   as repairable views. Reconciliation may `Swap` them to the chosen winner.
   Correctness must come from immutable contender history, not from a false
   `Create` result.
6. Because fabric data is currently in-memory and not migrated across a
   full-site restart, increment internal record versions directly. Do not build
   a migration framework.

## Request path

1. Validate the request and build its contender record.
2. Read the current projection before attempting a claim. Olric `Get` remained
   correct in the reproduced join window, so an already visible different claim
   can still return `409` immediately.
3. Persist the contender under its fingerprinted key before updating the
   projection. A false win can then damage only a repairable view, not erase the
   earlier proposal.
4. Preserve the existing exact-retry behavior for the same fingerprint.
5. Return `202` when no conflict is visible yet. This response still means only
   that a proposal was recorded, not that it is the eventual winner.

## Reconciliation

1. Group contenders by unit key on every pass.
2. Select the winner using the documented incumbent, observed-time, and
   fingerprint rules.
3. Reconcile every expected instance's confirmation against the selected
   proposal.
4. Mark every losing proposal effectively rejected with
   `registration_key_conflict`, even if it temporarily reached accepted.
5. Repair the current request and accepted projections to the winner. Repeated
   passes must converge after Olric membership stabilizes.
6. Avoid exactly-once event work in this stage. Existing event guarantees must
   be narrowed where a false create can duplicate a transition. Active conflict
   reporting is explicitly deferred; the query API in Stage 5 is the reporting
   mechanism for now.

## Unit and adapter tests

- An accepted incumbent remains winner when a later contender appears.
- Two pending contenders choose the earlier observed time.
- Equal observed times choose the same fingerprint on every machine.
- A losing contender that was temporarily accepted becomes rejected.
- Repeated and restarted reconciliation changes no final state.
- An exact retry does not create another contender.
- A visible conflict still returns `409` without disturbing the incumbent.
- Weak enumeration may delay detection for one pass but cannot change the final
  winner.
- Add a two-member Olric regression test that preserves both values through the
  known false-create window and eventually restores the winner projection.

## Exit criteria

- No registration correctness rule depends on exactly one successful `Create`
  for a shared unit key.
- Once all contenders are visible and membership is stable, every member derives
  the same winner and loser states.
- No coordination service, distributed lock, quorum, or leader is introduced.
- `task fast` passes.

