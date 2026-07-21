# Stage 01: Leadership domain

**Effort:** Large. **Complexity:** High. **Risk:** High.

The core of the plan. Every later stage exposes or consumes what this stage
decides. Nothing after it can start before its decision rule is settled.

## What is being decided

One machine per `unit_type` per site hosts the Active client service.

The leadership key is `(site, unit_type)`. Site is implied by the journal, since
each site has one journal and no cross-site uniqueness exists
(`docs/02-registration.md:159`), so the key reduces to a `uint8`.

The holder is a **machine**. It is not a platform process and not an individual
unit. Which process on the holding machine serves the role is already settled by
the machine fence, and adding a second process-level election on top of it would
create two mechanisms that can disagree about the same machine.

Only the fence holder claims, renews, and releases. The standby platform process
projects leadership state like any other journal fact but never contends.

## Why the registration rule cannot be reused

Registration accepts only on unanimous confirmation from every expected machine,
with no timeout and no forced acceptance (`docs/02-registration.md:69`). An
offline machine keeps a proposal pending forever, and the projection reports which
machine it is waiting for rather than deciding.

Leadership must be granted when a machine is gone. Under the unanimity rule a unit
type would have no Active machine for exactly as long as the failure lasts.

Leadership reuses the mechanics and replaces the rule:

| Reused from registration | Not reused |
| --- | --- |
| Event sourcing onto the site journal | Unanimous acceptance |
| Journal order as the tie-break | Waiting indefinitely on an expected machine |
| Deterministic identities, idempotent handlers | Absence of leases and expiry |
| Per-node projection, no node coordinating another | |

This introduces the platform's first time-based decision. Nothing in the current
domain expires. That is a real change to the system's character and should be
stated in the package documentation rather than discovered later.

## Decision rule

Claim, grant, renew, expire.

1. A candidate machine publishes `platform.leadership.claimed` for a unit type.
2. The first claim in journal order for a key with no live holder wins. Journal
   order is the tie-break, exactly as for registration keys
   (`docs/02-registration.md:97`).
3. The winner holds a bounded lease and publishes `platform.leadership.renewed`
   well inside it.
4. A holder that stops renewing has its lease expire. The key becomes claimable.
5. A holder stopping cleanly publishes `platform.leadership.released` and the key
   becomes claimable immediately, without waiting for expiry.

Every node folds the same ordered journal and derives the same holder, with no
reconciliation pass and no window in which two machines disagree about who holds
a key.

## The epoch is a fencing token

Every grant carries an epoch: the journal sequence of the granting event. It is
monotonically increasing, never reused, and never decreases.

The epoch exists because a demoted holder may not have learned it was demoted.
Expiry is a conclusion drawn by observers, and the former holder may be paused,
partitioned, or slow. Without a fencing token a stale Active can act after a new
Active has been granted.

- A unit emitting side effects with external ordering requirements carries the
  epoch with them.
- Anything receiving work stamped with an epoch lower than the highest it has seen
  rejects that work.
- A unit observing a leadership message with an epoch below its last known epoch
  discards it as stale.

That last rule is what makes the notification channel in stage 03 safe to lose,
duplicate, or reorder messages on. Correctness lives in the epoch, not in
delivery.

## Self-demotion precedes possible expiry

A holder that cannot renew must demote itself **before** its lease can expire
anywhere else, not after. The renewal deadline used locally is strictly earlier
than the expiry deadline used by observers, and the margin must exceed worst-case
journal write latency plus clock error.

Without this there is a window where the old holder still believes it is Active
and a new holder has been granted. Two simultaneous Actives is the failure this
entire feature exists to prevent, so the margin is a correctness parameter, not a
tuning knob, and its invariant is validated at configuration load rather than
documented.

## Clock assumption, stated explicitly

Leases assume machine clocks do not drift faster than the safety margin over a
lease period. This is a new assumption for this codebase. Use monotonic elapsed
time for local deadlines and journal order for cross-machine decisions, so clock
disagreement affects expiry timing only and never which event came first.

## Events

| Event | Stated by | Meaning |
| --- | --- | --- |
| `platform.leadership.claimed` | a candidate machine | offers to hold the unit type |
| `platform.leadership.granted` | the deciding handler | this machine holds it, from this epoch |
| `platform.leadership.renewed` | the holder | the lease continues |
| `platform.leadership.released` | the holder | clean handover, immediately claimable |
| `platform.leadership.revoked` | any node observing expiry | lease expired, claimable |

These follow the existing convention: facts stated by the package owning the
transition, published synchronously so the fact is in the journal before the
operation returns (`docs/01-architecture.md:213`).

`revoked` needs care. Several nodes observe the same expiry and will publish
concurrently. The deterministic decision-identity pattern already used for
registration decisions (`docs/02-registration.md:56`) collapses those onto one
fact and must be applied here.

## Open questions to settle before implementing

**Where does candidacy come from?** Two options, and the choice has consequences.

- From the descriptor: which unit types a machine may host is deployment data,
  known at build time, available even when the site is degraded. Consistent with
  how identity is already handled (`docs/01-architecture.md:38`).
- From accepted registrations: the registration projection already knows which
  machines host which unit types, so nothing new is authored. But acceptance
  requires unanimity, so candidacy would inherit the blocking property this stage
  exists to avoid.

Recommendation: candidacy from the descriptor, unit identity from registration.

**Does the grant cover every unit of that type on the machine?** If a machine
hosts several `unit_id` values of one `unit_type`, the recommendation is that all
of them become Active together, because the grant is to the machine. A deployment
needing per-`unit_id` single-active wants a different key, and that should be
refused for now rather than half-supported.

**Does a unit opt in?** Requirement OR-36 says services must be able to declare
single-active execution (`docs/drafts/requirements.md:87`). A unit type that does
not want leadership should not be given it. Declaration belongs in the descriptor
with candidacy.

## Configuration

Requirement OR-41 asks for configurable election behavior. Lease duration, renewal
interval, and the safety margin are site-adjustable runtime settings with the
margin invariant enforced at load time. Which unit types a machine may host is
deployment data and belongs in the descriptor, not runtime configuration.

## Work

1. New `platform/internal/leadership` package: key, epoch, lease, state machine,
   projection, durable handler.
2. Event types and envelopes in the `internal/events` convention.
3. Composition into the active runtime only. The standby projects and never
   claims.
4. Descriptor: hosted unit types and single-active declaration.
5. Config: lease, renewal, margin, with invariant validation.

## Tests

- The granted holder is unique across the site under concurrent claims.
- Journal order decides a contested claim identically on every node.
- Expiry grants the key to another machine.
- Clean release grants it without waiting for expiry.
- The epoch is strictly monotonic across grant, expiry, and regrant.
- A holder that cannot renew demotes before the observers' expiry deadline.
- Concurrent `revoked` publications from several nodes collapse to one fact.

## Out of scope

Cross-site leadership. Routing requests to the Active instance (OR-34, a MAY):
this stage makes it possible later and does not deliver it.
