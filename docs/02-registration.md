# Registration

Registration is a site-wide decision over the static deployment topology. A
client submits a proposal to one machine and polls that proposal until the site
decides it.

It is event-sourced. Every fact is an ordered event in the site journal, and
every answer is a projection of those facts that each machine folds for itself.
No query reads shared state, and no machine coordinates another.

## HTTP operations

| Operation | Meaning |
| --- | --- |
| `POST /registrations` | Validate a request and durably publish its proposal. `202` means the journal took it, not that the unit is registered. |
| `GET /registrations/{proposal_id}` | Read one proposal's projected state. This is the client's confirmation mechanism. |
| `GET /registrations` | List every proposal the site journal holds, including pending and rejected contenders. |
| `GET /registrations/conflicts` | Group competing proposals by unit key and identify the winner and rejected losers. |

Unit type and unit ID form the site-local key. The platform stamps its descriptor
machine and IP as the origin. A client cannot supply or override them.

## The asynchronous contract

The POST is purely asynchronous. Invalid input returns `400` and publishes
nothing. A durably published proposal returns `202` with its `proposal_id` and
the journal sequence it was given. A journal that will not accept the write
returns `503`: nothing was recorded, so the same request will succeed once the
site can write again, which makes it the one failure a client can act on.

There is no immediate `409`. A conflict is a projected outcome, not a
race-sensitive POST result: at the moment the journal accepts a proposal, nothing
has decided anything yet.

Status, list, and conflict answers come from the answering node's local
projection and are authoritative once that node has caught up — which it has
before it serves at all.

## Identity and idempotency

A proposal is identified by a `proposal_id`: a hash of the versioned canonical
request fields, the trusted origin identity, and the ordered expected machines.
Two identical requests are the same claim and produce the same ID, so a retry is
idempotent and returns the same handle; any different request is a separate
contender. A different origin is a different proposal, even for a byte-identical
request: the origin is part of what is being claimed.

A decision is identified by a `decision_id` derived from the proposal ID, the
decision kind, and the deciding machine, so a node that republishes its decision
after a redelivery collapses onto the one decision it already made — even after
any transport deduplication window has expired. Correctness lives in these
identities, not in transport deduplication.

Occurrence time and journal position are never part of an identity, so the same
fact always hashes the same way regardless of when or in what order it arrived.

## Acceptance boundary

A proposal is accepted only after every expected platform instance, including the
origin, confirms that exact proposal. Expected instances come from the static
descriptor. They are not a quorum and are not the currently reachable machines.

An expected machine that is offline keeps a proposal pending indefinitely. There
is no timeout, expiry, forced acceptance, or automatic removal from the
acceptance set. The projection says which machine it is waiting for rather than
going quiet.

## Event flow

| Event | Meaning |
| --- | --- |
| `platform.registration.proposed` | The origin proposes a registration after validating the HTTP input, its trusted origin, and the expected machine set. The first proposed event for a unit key in journal order claims that key. |
| `platform.registration.confirmed` | One expected node accepts the claiming proposal. |
| `platform.registration.rejected` | One expected node refuses a proposal, or a later proposal loses the key to an earlier one. Tagged as a warning. |
| `platform.registration.accepted` | The origin commits the registration once every expected node has confirmed the claiming proposal. |

A proposal carries its complete request, the trusted origin, and the ordered
expected machines, so a reader reconstructs a registration from the journal
alone.

Each node runs one durable registration handler. It consumes proposals and
confirmations, waits for its own projection to have applied the input, and
publishes exactly one deterministic consequence. Nothing delivers work to it that
it did not ask for, no node coordinates another, and there is no leader or
periodic scan.

## Ordering and conflict

Order is the site journal's order: when the journal accepted an event, not when a
client began its request. The first proposed event for a key permanently claims
it. An identical later proposal is a retry; a different later proposal is a
contender that is rejected with `registration_key_conflict`.

Every node folds the same ordered journal, so every node derives the same winner.
There is no reconciliation pass, no repair, and no window in which two machines
disagree about who holds a key.

A node rejecting the claiming proposal marks that proposal rejected but does not
release the key. Key release is a separate future domain event, not implicit
cleanup.

A structurally coherent proposal that fails current input or trusted-topology
validation is projected and receives a deterministic
`registration_invalid_proposal` rejection from each expected node. A proposal
with a forged identity, an impossible event order, or contradictory decisions is
invalid journal history: it stops replay and makes the node unready rather than
being skipped.

## Conflict visibility

`GET /registrations` returns each contender as a separate registration view. The
claiming proposal is pending or accepted. Every other proposal for that key is
rejected with `registration_key_conflict`.

`GET /registrations/conflicts` returns one resolved group per conflicting key,
with the unit key, the winner, and one or more losers. The query is domain
history, not liveness or readiness. A resolved conflict does not make a process
unhealthy.

Notifications, acknowledgement, pagination, filtering, retention policy, and
removal are not implemented.

## End-to-end example

An uncontested request in a two-machine site where node B starts late.

| Step | Where | Observation |
| --- | --- | --- |
| 1 | Client to node A | POST a unit proposal. |
| 2 | Node A | Validate it, stamp node A as origin, publish `proposed`, and return `202` with its proposal ID. |
| 3 | Node A's handler | Project the proposal, validate it, and publish node A's `confirmed`. |
| 4 | Client polls node A | Status is `pending`; node A has accepted and expected node B has not answered. |
| 5 | Node B starts | Replay the journal, find the proposal, and publish node B's `confirmed`. |
| 6 | Node A's handler | Observe every expected confirmation and publish `accepted`. |
| 7 | Client polls node A | Status is `accepted`; both machines list the same registration. |

If node B never starts, step 4 remains the answer indefinitely. A client may poll
either machine: both fold the same journal, so a proposal's status is the same
answer wherever it is asked.

For a conflict, the later proposal also receives `202` and appears in the site's
state. The earlier claim in journal order keeps the key, the later one is
projected as rejected, the list exposes both, and the conflicts query reports the
resolution. Every node derives that resolution independently and identically.

## Current limits

Registration state lives in the site journal and is replayed into memory at every
start, so it survives a restart but not a journal that is lost. Different sites
have separate journals, so cross-site uniqueness is not enforced. Per-instance
IPs in a view come from the answering node's own descriptor rather than from the
proposal, so a historical proposal naming a machine the deployment no longer has
reports that instance with an empty IP. Authentication, authorization, removal,
leases, heartbeats, and quorum are outside the current implementation.
