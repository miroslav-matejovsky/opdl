# Registration

Registration is a site-wide decision over the static deployment topology. A
client submits a proposal to one machine and polls that same machine for the
result.

## HTTP operations

| Operation | Meaning |
| --- | --- |
| `POST /registrations` | Validate and retain a proposal. `202` means taken, not accepted. |
| `GET /registrations/{unit_type}/{unit_id}/status` | Read the proposal submitted to this origin machine. This is the client's confirmation mechanism. |
| `GET /registrations` | List every retained proposal visible to the site, including pending and rejected contenders. |
| `GET /registrations/conflicts` | Group competing proposals by unit key and identify the winner and rejected losers. |

There is no request ID. Unit type and unit ID form the site-local key. The
platform stamps its descriptor machine and IP as the origin. A client cannot
supply or override them.

## Acceptance boundary

A proposal is accepted only after every expected platform instance, including
the origin, records acceptance of that exact proposal. Expected instances come
from the static descriptor. They are not a quorum and are not the currently
reachable fabric members.

An expected machine that is offline keeps the proposal pending indefinitely.
There is no timeout, expiry, forced acceptance, or automatic removal from the
acceptance set.

Each platform instance runs one reconciler. It scans proposals at startup and
then at `registration.reconcile_interval`, which defaults to one second. Nothing
delivers work to a reconciler, no instance coordinates another, and there is no
leader. Every pass derives and repairs state from shared records.

## Contenders and convergence

Every distinct proposal for a unit key is retained under a deterministic content
fingerprint. Confirmations and immutable acceptance markers are scoped to that
fingerprint. The single current request and accepted records are repairable
projections.

Reconciliation selects one winner:

1. A proposal accepted before a competitor was observed is the incumbent and
   remains the winner.
2. Without an accepted incumbent, the earliest platform-observed request time
   wins.
3. Equal times use the fingerprint as a deterministic tie-break.

Platform clocks are not coordinated. The time rule is a best-effort first-writer
rule, not a linearizable global order.

An identical repeat request is an idempotent retry. A different proposal that is
already visible at POST time returns `409 registration_key_conflict` and is not
retained. During an Olric membership change, a different proposal can pass a
temporarily false create result. It remains retained, and reconciliation later
marks it `rejected` with reason `registration_key_conflict` if it loses.

Once all contenders are visible and membership is stable, every reconciler
derives the same winner, repairs current projections to it, and exposes every
loser as rejected. A proposal can be briefly pending or accepted before that
correction. The query API, not domain event counts, is authoritative after
convergence.

## Conflict visibility

`GET /registrations` returns each contender as a separate registration view.
The selected proposal is pending or accepted. Every other proposal is rejected
with `registration_key_conflict`.

`GET /registrations/conflicts` returns one resolved group per conflicting key.
Each group contains the unit key, selected winner, and one or more losers. The
query is domain history, not liveness or readiness. A resolved conflict does not
make a process unhealthy.

Notifications, acknowledgement, pagination, filtering, retention policy, and
removal are not implemented.

## End-to-end example

The following sequence shows an uncontested request in a two-machine site where
node B starts late.

| Step | Where | Observation |
| --- | --- | --- |
| 1 | Client to node A | POST a unit proposal. |
| 2 | Node A | Retain the proposal, stamp node A as origin, record `requested`, and return `202`. |
| 3 | Node A reconciler | Validate the proposal and record node A's confirmation. |
| 4 | Client polls node A | Status is `pending`; node A is accepted and expected node B is pending. |
| 5 | Node B starts | Join the fabric, scan the retained proposal, validate it, and record node B's confirmation. |
| 6 | Node A reconciler | Observe all expected confirmations, write immutable acceptance evidence, and project the proposal as accepted. |
| 7 | Client polls node A | Status is `accepted`; both machines list the same registration. |

If node B never starts, step 4 remains the answer indefinitely. Node B never
becomes the request origin and its origin-specific status route returns `404`.

For a conflict during a join, the later proposal may initially receive `202` and
appear in site state. After reconciliation, the accepted incumbent remains the
winner, the later proposal is rejected, the list exposes both, and the conflicts
query reports the resolution. Reopening a reconciler derives the same result
from retained records.

## Current limits

Registration state is memory-only and is not replayed after a full-site
shutdown. Different sites have separate fabrics, so cross-site uniqueness is
not enforced. Authentication, authorization, removal, leases, heartbeats,
quorum, and persistence are outside the current implementation.

