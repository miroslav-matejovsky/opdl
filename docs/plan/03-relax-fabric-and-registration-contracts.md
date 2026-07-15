# Stage 3: Relax the fabric and registration contracts

**Estimate:** 0.5 to 1 day.

## Goal

Replace the strict uniqueness promise with an explicit eventual-conflict model
before implementing it.

## Decisions

### Fabric

`Collection.Create` remains create-if-absent while membership is stable. Across
a membership change it may temporarily report a false win and overwrite the
current value. Consumers that require uniqueness must retain contenders and
reconcile them after membership stabilizes.

The shared contract suite should continue to require one winner on a stable
fabric. It must stop presenting that property as unconditional across a join.

### Registration winner

Registration preserves every distinct proposal for a unit key. Reconciliation
chooses one winner by these rules:

1. A proposal accepted before another proposal was observed is the incumbent
   and remains the winner.
2. If proposals race before either is accepted, the earliest platform-observed
   request time wins.
3. Equal times use the proposal fingerprint as a deterministic tie-break.

Rule 2 is best effort across machines because clocks are not coordinated. This
is acceptable for the stated static deployment and low concurrency. The API and
documentation must not claim a linearizable global first-writer guarantee.

### Losing proposal

A proposal may appear accepted briefly. After all contenders are visible, the
reconciler changes the losing proposal's effective status to `rejected` with
reason `registration_key_conflict`. This reuses the existing terminal status and
avoids adding an ambiguous `unregistered` state.

### Visibility

- `GET /registrations` lists every preserved proposal, including rejected
  losers. A caller can see its final state through the existing API.
- Add `GET /registrations/conflicts` to group contenders by unit key and identify
  the winner and losers.
- Do not use readiness, liveness, or a general health endpoint. A resolved
  registration conflict does not make the process unavailable. There is no
  standard HTTP health representation that fits this domain history better than
  a domain-specific collection.
- Do not add notifications, acknowledgements, retention, or conflict removal in
  this change. Those are later reporting and lifecycle work.

## Work

1. Update `platform/internal/fabric/doc.go` and its contract-test descriptions.
2. Update `platform/internal/registration/doc.go` with contender retention,
   winner selection, convergence, and the clock limitation.
3. Update the root architecture overview and `docs/backlog/fabric.md` so the
   backlog item becomes the rationale for the relaxed contract, not an open
   strict-consistency defect.
4. Write test names and fixtures for the stages that follow before changing
   storage.

## Follow-on test design

| Test | Fixture | Expected observation |
| --- | --- | --- |
| `TestReconcileKeepsAcceptedIncumbent` | `acceptedIncumbent` and later contender | The incumbent remains accepted; the later proposal is rejected. |
| `TestReconcileChoosesEarliestPendingContender` | `twoPendingContenders` with injected distinct times | The earlier observed proposal wins. |
| `TestReconcileBreaksEqualTimesByFingerprint` | `sameTimeContenders` with an injected clock | Every member chooses the same fingerprint. |
| `TestReconcileRejectsTemporaryAcceptance` | `temporarilyAcceptedLoser` | A provisional acceptance becomes rejected with `registration_key_conflict`. |
| `TestJoinConvergesRetainedContenders` | `lateJoinOlricSite` with node A data before node B starts | Both proposals remain observable and the winner projection is restored after the join. |
| `TestListAndConflictQueryExposeResolution` | `resolvedConflict` | List returns both proposals; the conflict query groups winner and loser. |

Fixtures use a controllable service clock and retained proposal records. The
Olric test waits on observable state, never a fixed delay.

## Exit criteria

- Documentation makes no unconditional exactly-one-winner claim across joins.
- The stable-membership behavior remains precise and testable.
- Winner selection and the final loser state are deterministic from retained
  records.
- API visibility and intentionally deferred reporting features are explicit.

## Implementation result

Completed on 2026-07-15. Fabric and registration documentation now state the
stable-membership Create guarantee, contender convergence, best-effort ordering,
and the planned conflict query. The fabric contract test names now state their
stable-membership scope. Stage 4 implements storage and Stage 5 implements the
dedicated HTTP conflict query.
