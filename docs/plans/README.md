# Lease-based Primary Ownership

Status: Proposed. No code has been written for this plan; it exists to be
reviewed before any is.

This plan replaces the machine's non-expiring Windows named mutex with a
lease-based Primary Ownership mechanism, and gates promotion on peer health. It
implements the model described in [redundancy.md](../drafts/redundancy.md) and
the operational surface in [health.md](../drafts/health.md). Health endpoints are
already implemented (`platform/api`, `platform/internal/httpapi`); this plan
gives them real lease data to report and adds the ownership machinery behind
them.

It is deliberately staged so that the reporting surface can land before any
behavior changes, and the mutex is retired only in the final stage, after the
lease has proven itself.

## Why change a mechanism that works

Today an instance is Active because it holds a **non-expiring Windows named
mutex** in the machine-wide `Global\` namespace (`internal/redundancy/lock.go`,
`ownership.go`). The mutex is elegant and safe by construction:

- The kernel guarantees at most one holder, so two Active instances on one
  machine are impossible without any agreement between them.
- A crashed holder is released automatically, so failover after a crash is
  immediate and needs no timeout.
- A **paused but living** holder keeps ownership until its service manager
  terminates it, so it can never wake into a second Active instance.

That third property is exactly what the [redundancy.md](../drafts/redundancy.md)
draft asks us to give up. The draft wants **bounded failover from an
unresponsive-but-alive Primary**: a lease that expires while the holder is hung,
a Standby that promotes on expiry, and **health checks** as the gate that decides
whether promotion is allowed at all. A non-expiring mutex cannot express any of
this — a hung holder still holds the mutex, so the Standby can never take it.

So this is a genuine reversal of the earlier "use a non-expiring OS lock"
decision, made for a reason the earlier plan did not have: the draft now requires
recovery from a live-but-stuck Active, not only from a dead one. The rest of this
document is mostly about how we get the draft's failover behavior **without**
reopening the split-brain window the mutex closed.

The one structural fact that makes this tractable here, and that a general
distributed lease does not enjoy: **the Primary and Standby are two processes on
one machine.** They share a wall clock and a filesystem. There is no clock skew
to reason about and no network partition between owner and lease store. A lease
file on local disk, compared against the local clock, is as consistent as the
mutex was — the hard part of lease-based HA does not apply to a single-host pair.

## Objectives

- Make Primary Ownership a finite, renewable **lease** rather than a held kernel
  object. Ownership is valid only while the lease is unexpired.
- Renew the lease periodically while Active; lose ownership automatically when
  renewal stops.
- Gate promotion on **peer health**: a Standby promotes only when the lease has
  expired or been released **and** the peer's `/health` reports unhealthy.
- Support **failback** to a returning healthy Primary under a configurable
  policy, by graceful handover — never by seizing ownership.
- Track a monotonic **ownership generation** on every acquisition and report it,
  laying the groundwork for fencing without enforcing it yet.
- Populate the health API's existing lease fields (`leaseState`,
  `leaseExpirationUtc`, `ownershipGeneration`) with real values.
- Preserve the current split-brain guarantee: never two Active instances, even
  across process pauses and clock jumps.

## Non-goals

- Cross-machine or multi-node ownership. Ownership stays machine-scoped, exactly
  as the mutex was. A machine's two co-located instances are the only
  contenders.
- Fencing-token **enforcement**. Generation is tracked and reported; rejecting
  stale-token writes is deferred until there is a downstream consumer that needs
  it (see [Fencing](#fencing-and-ownership-generation)).
- Changing registration, the journal, or the domain surface. Only who is Active,
  and how that is decided, changes.
- Distributed clock synchronization. It is unnecessary for a single-host pair and
  its absence is a stated precondition, not a gap.

## Terms

| Term | Meaning |
| --- | --- |
| Lease | A machine-scoped record granting Primary Ownership for a finite duration. Held by at most one instance. |
| Lease Duration | How long a granted lease is valid without renewal. Draft example: 15s. |
| Renewal Interval | How often the owner extends the lease. Must be well below the duration. Draft example: 5s. |
| Step-down Grace | The margin before expiry at which an owner that has failed to renew must stop being Active. Guarantees it steps down before any promoter could see the lease expired. |
| Ownership Generation | A monotonic counter incremented on every acquisition. The draft's fencing token / ownership epoch. |
| Failover | A Standby taking ownership after the Active's lease lapses and the peer is unhealthy. |
| Failback | Ownership returning to a healthy Primary under the failback policy, by graceful handover. |
| Health-gated promotion | The rule that lease lapse alone does not permit promotion; the peer must also be unhealthy. |

## Current state, in code

The pieces this plan changes:

- `internal/redundancy/lock.go` — the Windows named mutex and its
  acquire/release/close operations. Retired in the final stage.
- `internal/redundancy/ownership.go` — `Contend`, `waitWhilePassive`,
  `activate`. The promotion state machine. Rewritten around the lease.
- `internal/redundancy/events.go` — the ownership event catalog. Extended.
- `internal/redundancy/role.go`, `state.go` — unchanged; role and state stay two
  axes, and Active still means "holds Primary Ownership."
- `internal/app/runtime.go` — `runProcess`, `runPassive`, `runActive`. Opens the
  lock and drives `Contend`. Rewired to open the lease and run the promotion
  loop.
- `config/config.go`, `config.toml` — no lease knobs today. Gains a
  `[redundancy]` section.
- `config/deployment.go` — the descriptor's `Lock` block carries only
  `windows_mutex`. Gains a machine-wide lease file path (builder change; see
  [Descriptor and builder impact](#descriptor-and-builder-impact)).
- `api/http.go` — `NewHealth` derives `leaseState` from Active/Passive and leaves
  `leaseExpirationUtc` nil and `ownershipGeneration` 0. Given a real lease view.

Ownership today flows through `Contend`: `TryAcquire` the mutex; if held, go
Active immediately; otherwise publish `OwnershipWaiting` and block in
`waitWhilePassive` on a kernel `Acquire`, which returns only when the holder
releases or dies. There is no periodic evaluation, no health check, and no
timeout. Note also that failback is not really implemented: a returning Primary
blocks as Passive until the Standby's process exits, because nothing asks the
Standby to hand back.

## Target design

### The lease store

A single **machine-wide lease file**, written atomically
(`utils/atomicfile`, already a vendor of `internal/redundancy`) and read by both
instances. Because both instances run on one host, the file's timestamps and the
reader's clock are the same clock; expiry is a local comparison, not a
distributed agreement.

Record (JSON, one object):

```json
{
  "generation": 42,
  "owner_role": "primary",
  "owner_pid": 12084,
  "acquired_unix_nano": 1753343700000000000,
  "expires_unix_nano":  1753343715000000000
}
```

- `generation` is monotonic across the file's life. Each acquisition writes
  `generation + 1`.
- `owner_role` / `owner_pid` identify the holder for diagnostics and for an owner
  to recognize its own lease on restart.
- `expires_unix_nano` is `now + lease_duration`, rewritten on every renewal.

Writes are compare-and-set on `generation`: acquisition and renewal read the
current record, verify the expected generation, and write the successor. A CAS
loss means another instance moved first, which the loser treats as "not owner."
On a single host this is a small critical section; the write is atomic-rename, so
a reader never sees a torn record. The mutex is retained through the early stages
(see [Stages](#stages)) as the CAS serializer, and only in the final stage is CAS
made to stand on its own.

There is deliberately no lease in memory that outlives the file: an instance's
belief that it is owner is only ever as fresh as its last successful read or
write of the record. This is what lets a hung owner discover, on waking, that it
was superseded.

### Ownership lifecycle

- **Acquire.** Permitted when the record is absent, expired, or explicitly
  released. The acquirer writes a new record with `generation + 1`, its identity,
  and a fresh expiry. Success makes it eligible to compose the Active runtime.
- **Renew.** The owner rewrites `expires_unix_nano` every renewal interval, same
  generation. A renewal that finds a different generation, or a different owner,
  means ownership was lost; the owner must step down (see safety below).
- **Validate.** Before it is meaningful to act as owner, and periodically while
  Active, an instance confirms the record still names it at its generation and is
  unexpired.
- **Release.** On graceful stop the owner writes an explicit release (expired
  record, same generation, or an empty owner) so the peer promotes without
  waiting a full duration. This is the fast path the mutex got for free from
  kernel abandonment.

### The promotion loop

Each instance runs one periodic loop, replacing today's block-on-`Acquire`:

```
every tick:
  read lease record
  if I am owner:
      renew; if renewal failed within step-down grace -> step down to Passive
  else (I am not owner):
      if lease is expired or released:
          if peer /health is unhealthy AND I am ready (caught up, within lag bound):
              acquire (generation+1); become Active
      else:
          remain Passive
```

- "peer `/health` is unhealthy" is a GET to the peer's loopback API address
  (`config.Instance.APIAddress`, already resolved per instance; see
  `app.peerOf`). Connection refused, timeout, non-200, or a body reporting
  `Unhealthy` all count as unhealthy. On one host over loopback this is cheap and
  its failure modes are unambiguous.
- "I am ready" reuses the existing failover-readiness signal
  (`app.startFailoverMonitor` / `FailoverReadinessChanged`): an instance that has
  not caught its projection up to within `lag_bound` is not promotable even if the
  lease is free and the peer is dead. Lease eligibility and catch-up readiness are
  independent gates and both must hold.

### Failover conditions

Promotion requires **both**:

1. The lease has expired or been released.
2. The peer is unhealthy.

The health gate is what keeps a slow renewal from causing a needless failover:
if the Active instance is alive and healthy but momentarily late to renew, the
Standby sees a healthy peer and does not promote, and the Active's own
step-down grace (below) keeps it from acting past its lease. Promotion is for a
peer that is actually gone or actually stuck, not for one that is merely slow.

### Failback

Under Preferred Primary, a healthy Primary should end up Active. When the Primary
returns while the Standby is Active:

- The Primary starts Passive (the Standby holds a valid lease).
- The failback policy decides whether the Standby hands back:
  - **automatic** (default): after the Primary has been continuously healthy for
    a stabilization window, the Standby gracefully releases — stops serving,
    writes a lease release — and the Primary acquires and becomes Active.
  - **manual**: the Standby holds ownership until an operator triggers handover;
    the Primary stays Passive but ready.
- Handover is always graceful and always release-then-acquire, never seizure. The
  stabilization window prevents ping-pong when a Primary is flapping.

This is the piece today's mutex model does not implement at all, so it is new
behavior rather than a reimplementation.

### Fencing and ownership generation

Every acquisition increments `generation`. It is written to the lease, carried on
`OwnershipAcquired`, and reported as `ownershipGeneration` on `/health/ha`. That
is the whole of this plan's fencing work: **track and report, do not enforce.**

Enforcement — stamping the generation on ownership-sensitive operations and
having consumers reject stale ones — is deferred because there is currently no
such consumer. Registration is journalled locally and the domain surface is
refused outright while Passive, so a stale owner has nothing to corrupt that the
step-down and health gate do not already cover. When ownership later controls an
external or shared resource, fencing enforcement becomes its own plan, and the
generation is already there to build on. This matches the draft, which calls
fencing optional and "strongly recommended when ownership controls" shared
state.

### Split-brain safety without the kernel mutex

This is the section the plan exists for. Removing the mutex must not reintroduce
two simultaneous Actives. Four mechanisms combine:

1. **Shared clock.** Owner and promoter read the same machine clock, so
   `expires_unix_nano` means the same instant to both. The classic lease hazard —
   the owner thinking the lease is valid while the promoter thinks it expired — is
   a clock-skew phenomenon, and there is no skew between two processes on one
   host. (Precondition: the lease uses the monotonic-adjusted wall clock; a
   backward wall-clock jump is discussed under [Risks](#risks-and-open-questions).)

2. **Owner self-step-down before expiry.** An Active instance that fails to renew
   by `expires_unix_nano - step_down_grace` must stop serving and drop to Passive
   **on its own**, before any promoter is allowed to consider the lease expired.
   The promoter only acts at `expires_unix_nano`. The grace is sized to cover a
   renewal attempt plus its timeout, so the owner has stepped down before the
   promoter looks. This is the primary guarantee; the rest are defense in depth.

3. **Health gate.** Even at expiry, the Standby promotes only if the peer is
   unhealthy. A live Active that stepped down cleanly answers `/health` and blocks
   promotion until it has actually released, at which point the release makes
   promotion correct.

4. **Generation on wake.** The one case the mutex handled that a lease cannot fully
   prevent: an Active process frozen past its lease (deep pause, VM suspend),
   during which the Standby promoted, then unfrozen still believing it is Active.
   Here the shared clock and step-down do not help, because the frozen process ran
   no code. On waking it re-reads the lease before any ownership-sensitive action,
   finds a newer `generation` and a different owner, and self-fences: it steps
   down immediately and takes no Active action. Because the domain surface is
   refused while non-owner and registration is local, the exposure between wake
   and re-read is bounded to in-memory work with no external effect — which is
   precisely the case the draft says fencing is optional for. Should that change,
   generation enforcement (already tracked) is the follow-up.

The honest summary: the mutex made the frozen-wake case impossible; the lease
makes it detectable and harmless given today's surface, and enforceable later.
Everything else the mutex did — crash failover, clean handover, single Active —
the lease does at least as well, and it adds the hung-primary failover and
failback the mutex could not.

### Retiring the Windows mutex

Once the lease's CAS carries ownership on its own (final stage), the mutex is
removed: `OpenLock`, the `Lock` type, the `lock` vendor usage, and the
descriptor `windows_mutex` field. Until then it is retained as the CAS serializer
and as a belt-and-suspenders local guard, so no stage before the last one weakens
the current guarantee.

## Configuration

A new `[redundancy]` section in the TOML file, validated in `config.go` beside
the existing timeouts. All required, no defaults, matching the file's stated
"no implicit defaults" policy.

```toml
[redundancy]
# How long a granted lease is valid without renewal.
lease_duration = "15s"
# How often the owner renews. Must be well below lease_duration.
lease_renewal_interval = "5s"
# How often a Passive instance evaluates promotion and polls peer health.
health_check_interval = "2s"
# Failback to a returning healthy Primary: "automatic" or "manual".
failback_policy = "automatic"
# How long the Primary must be continuously healthy before automatic failback.
failback_stabilization = "30s"
```

Validation rules:

- Every duration positive (existing `validateDuration`).
- `lease_renewal_interval < lease_duration`, with enough headroom for the
  step-down grace (proposed: renewal interval no more than one third of the
  duration, so at least two renewal attempts fit before expiry).
- `failback_policy` one of `automatic`, `manual`; `failback_stabilization`
  required only for `automatic`.
- The section is required whenever the machine deploys a Standby (the same
  condition under which `lock` is required today). A standby-less machine has no
  contender and needs none of it, mirroring how `lock` is omitted there.

The step-down grace is derived, not configured, to keep the invariant
`renewal_interval < duration - grace` impossible to misconfigure: propose
`grace = lease_duration - lease_renewal_interval - (one renewal timeout)`, stated
explicitly in the design of the lease type.

## Descriptor and builder impact

The lease needs a machine-wide file path both instances resolve to the same
value, the way both resolve the same `windows_mutex` today. The two instances
have separate `data_dir`s (`config.Instance.DataDir`) and the machine has no
shared root in the descriptor, so this is a **new resolved field**:

- Replace (or, during transition, sit beside) `descriptor.Lock.WindowsMutex`
  with a `lease_file` path under a machine-wide directory the builder owns.
- This is a **builder change** (`builder/`, a separate module) as well as a
  platform one. The platform reads the path as identity, exactly as it reads
  `windows_mutex` today; it never composes one itself.
- `config/deployment.go`'s `UnmarshalJSON` gains a required-field check for the
  new path under the same standby condition, and the conformance-tests module's
  descriptor-compatibility check is updated.

Flagging this early because it crosses the module boundary and gates Stage 1: the
lease store cannot be opened until the descriptor tells the platform where it
lives.

## API and health surface

The health contract already carries the fields; this plan fills them:

- `api.NewHealth` gains a lease-view source (a `func() LeaseView` supplied by the
  runtime) instead of deriving `leaseState` from Active/Passive alone. It reports
  real `leaseState`, `leaseExpirationUtc` (from `expires_unix_nano`), and
  `ownershipGeneration`.
- `HealthHAResponse` may gain an optional `fencingToken` mirror of the generation
  if that name is preferred operationally; the draft treats the two as synonyms.
- `/health/ready` stays as-is; readiness is projection catch-up, not lease state.
  `/health` and `/health/ha` are the ones that gain lease detail.

These are the "small adjustments" the health work anticipated; no new endpoint.

## Events

Extend `internal/redundancy/events.go` (`platform.redundancy.*`) so the local
record tells the whole ownership story:

- `lease_acquired` (carries generation, whether the prior lease was expired,
  released, or absent) — subsumes/augments `OwnershipAcquired`.
- `lease_renewed` — stated on renewal only when something notable changes, not
  every 5s, to keep the append-only record from growing on a heartbeat (the same
  discipline `FailoverReadinessChanged` already follows).
- `lease_renewal_failed` — a renewal that did not complete; carries how close to
  expiry.
- `stepped_down` — an owner dropped to Passive because it could not renew.
- `promotion_declined` — evaluated but not promoted, with the reason (lease still
  valid, or peer healthy, or not caught up). Valuable for failover
  troubleshooting.
- `failback_initiated` / `failback_completed` — the Standby handed ownership back
  to the Primary.

Severity follows the existing pattern: renewal failure and step-down are warnings,
a completed failover from an unhealthy peer is informational (it worked), an
activation failure stays an error.

## Stages

Each stage compiles, passes tests, and leaves the machine correct. Behavior does
not change until Stage 3, and the mutex is not removed until Stage 5.

1. **Lease store and reporting, behind the mutex.**
   Add the descriptor `lease_file` (builder + platform), the lease record type,
   and atomic read/write with CAS. The mutex still decides ownership; on
   acquisition, also write a lease record and bump the generation. Populate the
   real `/health/ha` fields from it. No promotion behavior changes.
   *Done when:* `/health/ha` shows a real generation and expiry on a running
   machine, and the store has unit tests for CAS, expiry, and atomic writes.

2. **Renewal and self-step-down.**
   The Active runtime renews every interval and steps down to Passive if it
   cannot renew within the grace. Still behind the mutex, so a step-down releases
   the mutex too and the existing waiter promotes. This proves renewal and
   step-down in isolation before they carry ownership.
   *Done when:* a paused-renewal fault (test hook) makes the Active step down and
   the Standby take over, all via existing mutex handover.

3. **Health-gated promotion loop.**
   Replace `waitWhilePassive`'s block-on-`Acquire` with the periodic loop:
   evaluate lease + peer health + readiness, acquire on eligibility. The mutex is
   still acquired under the lease as a local guard, but the *decision* is the
   lease's. Failover from a hung-but-alive Primary now works.
   *Done when:* a hung Active (renewal stopped, process alive, `/health`
   unhealthy) is failed over from within a lease duration; a slow-but-healthy
   Active is not.

4. **Failback.**
   Add the failback policy and stabilization window; the Standby hands back to a
   returning healthy Primary. *Done when:* a Primary restarted after a failover
   reclaims Active automatically after the stabilization window under
   `automatic`, and holds Passive under `manual`.

5. **Retire the mutex.**
   Make the lease CAS stand alone as the ownership authority; remove `OpenLock`,
   `Lock`, the `winmutex` usage, and the descriptor `windows_mutex`. *Done when:*
   ownership is decided solely by the lease, the split-brain scenarios still pass,
   and the mutex code is gone.

## Testing

- **Unit** (`internal/redundancy`): lease record round-trip, CAS win/loss, expiry
  boundary, step-down grace arithmetic, promotion-loop decision table (lease
  state × peer health × readiness → promote/decline). Follow the existing
  discipline: no `require.*` inside an `Eventually` condition goroutine.
- **Scenarios** (`scenarios/resilience`, `scenarios/standby`): failover from a
  crashed Active (fast path via release), from a hung-but-alive Active (lease
  expiry + unhealthy peer), no-failover from a slow-but-healthy Active, automatic
  and manual failback, and the frozen-wake self-fence. Scenarios rebuild the
  binary at run time, so after editing `platform/**` run them with `-count=1`;
  pass `diagnostics(machines...)` for a dump captured at the moment of failure.
- A dedicated split-brain assertion: at no observed instant do both instances
  report `runtimeState: active` on `/health`.

## Risks and open questions

- **Backward wall-clock jump.** A large backward step could make an unexpired
  lease look valid longer, or a fresh one look expired. Mitigation: base
  `expires` on a monotonic-adjusted clock, or store a monotonic reference
  alongside the wall time. Decide in the lease type's design; call it out because
  the shared-clock safety argument depends on it.
- **Renewal timeout vs. disk stalls.** A slow atomic write (fsync on a stalled
  disk) could delay a renewal into the grace window and cause an unnecessary
  step-down. The grace must budget for a realistic worst-case write, and the
  numbers (15s / 5s from the draft) should be validated against the target
  hardware rather than adopted verbatim.
- **Failback flapping.** A Primary that is healthy in bursts could trigger
  repeated handovers. The stabilization window addresses it; its default needs an
  operational value, and `manual` exists as the escape hatch.
- **Descriptor path ownership.** The machine-wide lease directory must be on a
  local filesystem (the same constraint the old file-lock plan stated) and shared
  by both instances but by nothing else. The builder resolves it; the platform
  must fail closed if the path is missing or not local.
- **Two evaluators, one file.** Both instances writing the lease relies on the
  atomic-rename + CAS discipline holding without the mutex in Stage 5. The
  single-host, two-writer case is simple, but the CAS retry/loss paths need
  explicit tests before the mutex is removed.

## References

- [redundancy.md](../drafts/redundancy.md) — the ownership, promotion, failover,
  failback, and fencing model this implements.
- [health.md](../drafts/health.md) — the `/health` and `/health/ha` surface the
  lease feeds.
- `platform/internal/redundancy/` — the mechanism this replaces
  (`lock.go`, `ownership.go`, `doc.go`, `events.go`).
- `platform/internal/app/runtime.go` — where ownership is opened and driven.
- `platform/api/http.go` — `NewHealth`, the lease fields to populate.
