# Stage 04: Failback policy

**Effort:** Large. **Risk:** High. **Depends on:** stage 01. Couples to stage 07.

The only stage in this plan that changes behavior. Every other stage is naming,
structure, or documentation.

## Status: deferred

Decided: skip for now. Behavior is unchanged, no policy is added to the blueprint,
and failback remains an operator procedure with no configuration.

The rest of the plan continues without it. Stage 05 still adopts the word
**failback**, which is correct for a manual procedure: failback names the
transition, not its trigger, and manual failback is an ordinary arrangement. To
keep the word from implying more than the platform does, stage 05 and the runbooks
state the trigger wherever they use it: **ownership returns only when an operator
or deployment tooling stops the Active instance.**

Everything below stands as the analysis for when this is picked up. The two facts
that shape it, and are easy to lose:

- Automatic failback needs a way for a returning Primary Instance to *ask* for
  ownership, because the mutex has no preemption by design. The two instances have
  no channel to ask through, so this means adding inter-instance communication to
  an architecture that deliberately has none.
- Both instances are on one machine, so failback buys operational uniformity, not
  capability. An Active Standby is not degraded. The cost is a real interruption
  of roughly one Ownership Transfer.

And the trap: automatic failback would fight the rolling upgrade, sending
ownership back to the un-upgraded instance the moment step 4 transfers it away.

## Why this stage exists

The Preferred Primary policy states that after recovery, upgrade, or maintenance,
ownership returns to the Primary Instance according to the configured failback
policy.

**Neither half of that is implemented.** Ownership does not return automatically,
and there is no failback policy to configure.

## Current behavior, verified

| Step | Result |
| --- | --- |
| Both instances running | Primary `active`, Standby `standby` |
| Primary Instance stops | Standby becomes `active`, automatically |
| Primary Instance returns | Primary sits at `state=standby` indefinitely; Standby stays `active` |

The returning Primary Instance calls `TryAcquire`, finds ownership held, and falls
through to `runStandby` (`platform/internal/app/runtime.go:87-99`). There is no
preemption path.

The scenario asserts exactly this
(`scenarios/warm_standby_test.go:92-95`):

```go
primarySecond := node.startManaged(ctx, t, "primary", manifest.Primary.Args)
node.waitStatus(t, primarySecond, "standby", true)   // the returning primary waits
standbySecond.stopGracefully(t)                      // an external action is required
node.waitStatus(t, primarySecond, "active", false)   // only now
```

`TestPromotionAndPrimaryReclamation` does the same, commenting that "Deployment
keeps the primary preferred by gracefully stopping the promoted standby."

So the platform today implements a hardcoded manual-only failback policy. That is
deliberate and documented: `docs/01-architecture.md:317` states the primary never
steals ownership from a live standby.

## Two facts that shape the design

### Automatic failback needs preemption, and nothing can preempt

The ownership mutex has no expiry and no preemption by design
(`utils/winmutex/doc.go`). A holder releases voluntarily or dies. That property is
what prevents split-brain, and it must not be weakened.

So a returning Primary Instance cannot take ownership. The Active Standby has to
give it up, which means the returning Primary must be able to **ask**.

**The two instances have no channel to ask through.** They never communicate:
there is no handshake, heartbeat, negotiation, or message of any kind between
them, and all coordination is mediated by the ownership object. Automatic failback
therefore requires introducing inter-instance communication into an architecture
that deliberately has none. That is the real cost of this stage, and it is why it
is Large rather than Medium.

### Both instances are on the same machine, so failback buys uniformity, not capability

Unlike a classic primary/standby pair spread across machines, these two processes
share one machine, one set of endpoints, one journal, one configuration, and the
same hardware. Neither is faster, closer, or better provisioned than the other.

An Active Standby is not a degraded state. It serves identically.

What failback buys is **operational predictability**: the fleet stays uniform, and
an operator seeing the standby service Active knows something happened rather than
having to remember which machine is arranged which way. That is a real benefit and
a modest one, and it is paid for with a deliberate service interruption of roughly
one Ownership Transfer, measured at about 164 ms.

This matters for choosing the default. Automatic failback trades availability for
tidiness, on a pair where the tidiness is the only difference.

## Target

A configured failback policy with at least two modes.

| Mode | Behavior |
| --- | --- |
| `manual` | Ownership returns only when the Active instance is stopped by an operator or by deployment tooling. Today's behavior, made explicit. |
| `automatic` | A returning Primary Instance requests ownership once it is safe to take it, and the Active Standby releases cooperatively. |

Recommendation: **`manual` as the default**, given the tradeoff above, with
`automatic` available for deployments that value uniformity over a short
interruption. This is a decision below, because the stated policy language implies
automatic is expected.

### Mechanism, if automatic is adopted

Preserve the ownership invariant exactly: the mutex is never preempted, and the
holder always releases voluntarily.

1. The returning Primary Instance runs as a waiter, as it does today, and catches
   up its projection.
2. Once it is safe to take over, it signals a **failback request**: a second named
   kernel object beside the ownership mutex, in the same `Global\` namespace and
   derived from the same identity.
3. The Active Standby waits on that object while serving. On signal, and only when
   its own role is standby and the policy is automatic, it performs the same clean
   shutdown it performs for a service stop: HTTP intake drains, handlers stop, the
   stopping event is stated, the projector stops, the transport closes, and only
   then is ownership released.
4. The waiting Primary Instance's acquisition returns and it activates.

A named event is the right primitive here for the same reasons the named mutex
was: machine-scoped, kernel-managed, no filesystem, no new transport, and it
disappears with the processes. It does not reintroduce a filesystem coordination
mechanism.

### Safety conditions before requesting failback

A failback that produces a broken Active instance is worse than no failback. The
returning Primary Instance must not request ownership unless:

- its projection is caught up within the configured lag bound;
- it has no last error;
- it can reach the journal;
- it has been stable for a configured minimum period.

The last one is the flapping guard. A crash-looping Primary Instance under an
automatic policy would otherwise cause repeated interruptions, each one a real
outage, while the Standby that was serving correctly is repeatedly displaced.

## The upgrade conflict

This is the finding most likely to be missed, and it makes automatic failback
unsafe without a suppression mechanism.

`docs/operations/upgrade.md` rolling upgrade, steps 2 through 5: upgrade the
Standby, transfer ownership to it, then upgrade the now-standby former Primary.

Under an automatic policy, the moment step 4 transfers ownership to the upgraded
Standby, the old Primary Instance would request it straight back, before step 5
has upgraded it. Automatic failback fights the upgrade procedure and would leave
the machine running its **old** binary Active.

So automatic failback requires one of:

- a suppression or maintenance mode the upgrade procedure sets and clears;
- failback requests being refused while the two instances report different
  binary versions;
- the upgrade procedure switching the policy to manual for its duration.

Whichever is chosen, `docs/operations/upgrade.md` must be rewritten alongside this
stage. It currently assumes ownership stays where it is put.

## Decisions

**D1. Default mode.** `manual` as recommended, or `automatic` to match the stated
Preferred Primary policy? The tradeoff is a deliberate interruption in exchange
for fleet uniformity, on two instances that are otherwise identical.

**D2. Is `automatic` in scope at all right now?** A smaller change is to keep
today's behavior, name it `manual`, document that failback is an operator
procedure, and defer the mechanism. That delivers the vocabulary and the honest
description without the inter-instance channel. Everything else in this plan could
then proceed.

Recommendation: consider this seriously. It removes the only Large, High-risk item
from the plan, and the manual procedure is already documented and tested.

**D3. The re-contention race.** After the Active Standby releases in response to a
request, both it and the returning Primary are contenders. Windows mutex wait
ordering is approximately fair but not guaranteed FIFO, so the Standby could
re-acquire and flap.

Options: have the Standby back off until it observes the Primary holding
ownership; have it exit and let the service manager restart it as a waiter, which
is closest to today's manual procedure; or gate re-contention on the failback
request being cleared. Needs a decision before implementation.

**D4. Upgrade suppression.** Which of the three options above? Recommendation:
refuse failback requests while the instances report different versions, because it
needs no operator action and cannot be forgotten. It requires the instances to
know each other's version, which the status files already carry PIDs and state
for but not a version.

**D5. Where does the policy live?** It is local high-availability policy, so it
belongs in the blueprint's `standby` block alongside the ownership configuration
that stage 07 moves there:

```hcl
standby {
  disabled = false
  ownership { namespace = "opdl" }
  failback { mode = "manual" }
}
```

If so, stage 07 and this stage should land together so the block is restructured
once.

**D6. A third mode?** A windowed policy, automatic only during a maintenance
window, is a plausible request. Recommendation: do not build it now; note it as
possible so the configuration shape can accommodate a mode string rather than a
boolean.

## Work

Scope depends on D2. If `automatic` is in scope:

1. Failback policy in the blueprint, descriptor, and validation, with stage 07.
2. A named failback-request object in `utils/winmutex` or a sibling package,
   created from the same derived identity as the ownership object.
3. Request signalling in the waiting Primary Instance, gated on the safety
   conditions and the stability period.
4. Request handling in the Active Standby, reusing the existing clean shutdown
   path so release ordering is unchanged.
5. Re-contention handling per D3.
6. Upgrade suppression per D4.
7. Operational events: failback requested, failback declined with a reason,
   failback completed.
8. Rewrite `docs/operations/upgrade.md` for the policy.

If only `manual` is in scope, the work is items 1, 7 in reduced form, and a
documentation pass stating that failback is an operator procedure.

## Validation

- A scenario covering the exact sequence: both running, Primary stops, Standby
  becomes Active, Primary returns. Under `manual`, ownership stays with the
  Standby and the Primary reports `role=primary, state=standby`. Under
  `automatic`, ownership returns without operator action.
- A crash-looping Primary Instance under `automatic` does not cause repeated
  interruptions.
- A rolling upgrade completes correctly under `automatic`, ending with both
  instances on the new binary and the Primary Active.
- Ownership is never held by two instances at any point, which is the invariant
  the cooperative release exists to preserve.
- `task all` passes.

## Consequence for stage 02

Stage 02 lists `role=primary, state=standby` as a state meaning "failback has not
happened yet". Under `manual` that is a **persistent steady state**, not a
transient one: a machine can sit there indefinitely with a healthy, idle Primary
Instance. The monitoring runbook must say so, and should say whether it warrants
an alert. Under `automatic` it is genuinely transient and a long dwell time is a
fault worth alerting on.
