# Stage 06: Failback policy

**Effort:** Large. **Complexity:** High. **Depends on:** stage 05.

## Intent

The goal states that after recovery, ownership is transferred back **according to
the configured failback policy** (goal:53-58).

Neither half is implemented. Ownership does not return on its own, and there is no
policy to configure.

## Current behavior, verified

| Step | Result |
| --- | --- |
| Both instances running | Primary Active, Standby Passive |
| Primary Instance stops | Standby becomes Active, automatically |
| Primary Instance returns | Primary sits Passive indefinitely; Standby stays Active |

The returning Primary calls `TryAcquire`, finds ownership held, and falls through
to the waiting path in `platform/internal/app/runtime.go`. There is no preemption.

The warm standby scenario asserts exactly this: the returning primary waits, an
external action stops the Active standby, and only then does the primary activate.

So the platform implements a hardcoded manual-only failback policy. It is
deliberate and documented; it is simply not the configured policy the goal
describes.

## The two facts that shape this

### Nothing can preempt, so the returning Primary must ask

The ownership mutex has no expiry and no preemption, by design
(`utils/winmutex/doc.go`). A holder releases voluntarily or dies. That property is
what prevents split brain and it must not be weakened.

So a returning Primary Instance cannot take ownership. The Active Standby has to
give it up, which means the Primary must be able to **ask**.

Under the pre-stage-05 architecture the two instances had no channel to ask
through, which is what made this stage expensive. **Stage 07 changes that**: it
introduces Named Pipes, and the goal explicitly allows them for "Primary/Standby
coordination, diagnostics, or health visibility" (goal:76).

There is a hard constraint attached, from the same section: **ownership decisions
must always be based on mutex ownership and never on IPC state** (goal:77). A pipe
may carry a *request*. It may never carry the *decision*. The decision remains: the
holder releases, and whoever then acquires the mutex is Active.

That is the invariant to review this stage against.

### Both instances are on one machine, so failback buys uniformity

These two processes share a machine, a site, and now their own equivalent journals.
Neither is faster, closer, or better provisioned. An Active Standby is not a
degraded state; it serves identically.

What failback buys is operational predictability: the fleet stays uniform, and an
operator seeing a standby service Active knows something happened rather than
having to remember which machine is arranged which way. That is a real benefit and
a modest one, paid for with a deliberate interruption of roughly one Ownership
Transfer, previously measured at about 164 ms.

This is why `manual` is a defensible default even though the goal's wording implies
automation.

## Target

| Mode | Behavior |
| --- | --- |
| `manual` | Ownership returns only when the Active instance is stopped by an operator or by deployment tooling. Today's behavior, made explicit and named. |
| `automatic` | A returning Primary Instance requests ownership once it is safe, and the Active Standby releases cooperatively. |

The policy belongs under `standby {}`, beside the lock stage 03 puts there:

```hcl
standby {
  disabled = false

  lock     { windows_mutex = "Global\\opdl-customer-a-north-local-server" }
  failback { mode = "manual" }

  api        { port = 8081 }
  winservice { name = "..." }
  nats       { client_port = 4322  cluster_port = 6322 }
}
```

For the same reason the lock is there: **failback only exists where a standby
does.** A machine that deploys no Standby Instance has nothing to fail over to and
nothing to fail back from, so a failback policy on it would state a decision that
can never take effect — which is the rule every block in `standby {}` follows.

A mode string rather than a boolean, so a windowed policy can be added later
without changing the shape.

In the descriptor, the same reasoning as the lock applies: failback is a machine's
runtime policy, read by whichever instance is running, so it resolves beside the
lock rather than inside one instance's record. Settle it with stage 03's D1 so both
move once.

## Mechanism, if automatic is adopted

1. The returning Primary runs as a waiter and catches up its projection.
2. Once safe, it signals a failback request.
3. The Active Standby, seeing the request and only when its own role is standby and
   the policy is automatic, performs its normal clean shutdown: HTTP intake drains,
   handlers stop, the stopping event is stated, the projector stops, the transport
   closes, and only then is ownership released.
4. The waiting Primary's acquisition returns and it activates.

**Signalling primitive.** A named kernel event beside the lock, in the same
`Global\` namespace and named from the authored mutex name, is the option that adds
nothing new: machine-scoped, kernel-managed, no filesystem, disappears with the
processes. A Named Pipe from stage 07 is the other. Decision D3.

Note that after stage 03 the lock's name is authored rather than derived, so a
request object named from it inherits whatever the blueprint author wrote. Suffix
the authored name rather than deriving a fresh one, so the pair is visibly related
in a `handle.exe` listing and a machine cannot end up with a lock and a request
object that belong to different deployments.

### Safety conditions before requesting

A failback that produces a broken Active instance is worse than no failback. The
returning Primary must not request unless its projection is within the lag bound,
it has no last error, it can reach the journal, and it has been stable for a
configured minimum period.

The last is the flapping guard. A crash-looping Primary under an automatic policy
would otherwise cause repeated real outages while the Standby that was serving
correctly is repeatedly displaced.

## The upgrade conflict

The finding most likely to be missed, and it makes automatic unsafe without
suppression.

`docs/operations/upgrade.md` describes a rolling upgrade: upgrade the Standby,
transfer ownership to it, then upgrade the now-passive former Primary.

Under an automatic policy, the moment ownership transfers to the upgraded Standby,
the old Primary requests it straight back, before it has been upgraded. Automatic
failback fights the upgrade and leaves the machine running its **old** binary
Active.

So automatic requires one of: a suppression mode the upgrade sets and clears;
refusing requests while the instances report different binary versions; or the
upgrade switching the policy to manual for its duration.

Whichever is chosen, `docs/operations/upgrade.md` is rewritten with this stage. It
currently assumes ownership stays where it is put.

## Decisions

**D1. Is `automatic` in scope now?** The smaller change is to name today's
behavior `manual`, make it configurable, document it, and defer the mechanism. That
delivers the configured policy the goal asks for without the request channel.

Recommendation: consider it seriously. It removes the only Large, High-complexity
item that is not already required by stage 05, and manual failback is an ordinary
arrangement. Note that stage 07 lands the channel anyway, which makes `automatic`
cheaper afterwards than it is now.

**D2. Default mode.** `manual`, given the tradeoff above.

**D3. Signalling primitive**, if automatic is in scope: named kernel event or the
stage 07 pipe? Recommendation: named event. It keeps the ownership path free of the
IPC layer entirely, which makes the goal:77 invariant obvious by construction
rather than by review.

**D4. Re-contention.** After the Standby releases, both it and the returning
Primary are contenders, and Windows wait ordering is approximately fair but not
FIFO. The Standby could re-acquire and flap. Options: back off until it observes
the Primary holding; exit and let the service manager restart it as a waiter, which
is closest to today's manual procedure; or gate re-contention on the request being
cleared.

**D5. Upgrade suppression.** Recommendation: refuse requests while the instances
report different versions. It needs no operator action and cannot be forgotten. It
requires the instances to know each other's version, which the status files do not
carry today.

## Work

If only `manual` is in scope: the blueprint and descriptor policy field, its
validation, an operational event when a failback opportunity is declined by policy,
and a documentation pass.

If `automatic` is in scope, additionally: the request object per D3, request
signalling gated on the safety conditions, request handling in the Active Standby
reusing the existing clean shutdown path, re-contention per D4, suppression per D5,
events for requested/declined/completed, and the upgrade runbook rewrite.

## Validation

- `task all` passes with the gate enabled.
- A scenario covers the full sequence: both running, Primary stops, Standby becomes
  Active, Primary returns. Under `manual` ownership stays put and the Primary
  reports `role=primary, state=passive`. Under `automatic` it returns with no
  operator action.
- A crash-looping Primary under `automatic` causes no repeated interruptions.
- A rolling upgrade completes correctly under `automatic`, ending with both
  instances on the new binary and the Primary Active.
- Ownership is never held by two instances at any point.
- No code path reads IPC state to decide ownership.
