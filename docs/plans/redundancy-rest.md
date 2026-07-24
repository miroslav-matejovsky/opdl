# Lease-based ownership: what is left

Status: the lease and health-gated promotion described in
[README.md](README.md) are implemented. The Windows named mutex is gone; Primary
Ownership is a renewable lease file authored under a machine's `platform.standby`
block. This document records what that pass deliberately left out, so the next
one starts from a written baseline rather than from reading the code.

## Implemented

- A machine-wide lease file with an ownership generation, acquire/renew/release,
  and expiry (`platform/internal/redundancy/lease.go`).
- Renewal while Active, and self-step-down when the owner cannot renew before its
  lease would lapse (`ownership.go`, `startRenewal`).
- Health-gated promotion: a Standby takes over only when the lease has lapsed
  **and** the peer's `/health` reports it cannot serve. The gate is an
  app-supplied callback, so `redundancy` makes no HTTP call itself
  (`platform/internal/app/lease.go`, `peerHealthCheck`).
- Real `/health/ha` reporting: `leaseState`, `leaseExpirationUtc`, and
  `ownershipGeneration` come from the live lease.
- Blueprint authoring: `platform.standby.lease { file, duration,
  renewal_interval, health_check_interval, failback_stabilization }`, resolved
  into the descriptor and validated by the builder and the platform.
- Ownership events: `lease_opened`, `ownership_waiting`, `ownership_acquired`
  (with generation), `promotion_declined`, `lease_renewal_failed`,
  `stepped_down`, and the activation events.

## Deferred

### Failback to a returning Primary (Preferred Primary)

The draft's Preferred Primary policy wants a healthy Primary to end up Active
after it returns. This pass does not implement it: a Primary that restarts while
the Standby is Active becomes Passive and stays there until the Standby stops.

`failback_stabilization` is authored, carried in the descriptor, and validated,
but nothing reads it yet — it is reserved for this work. When it lands:

- The Standby, seeing the Primary healthy for the stabilization window, gracefully
  releases (stops serving, writes a released lease) so the Primary reacquires.
- A policy switch (`automatic` vs `manual`) decides whether that happens on its
  own or waits for an operator.
- The handover must be release-then-acquire, never seizure, and must not
  ping-pong when a Primary is flapping.

The health-gate callback and the released-lease fast path are already in place,
so this is mostly a second evaluation loop on the Active side plus the policy.

### Fencing-token enforcement

Every acquisition bumps `generation`, and it is written to the lease, carried on
`ownership_acquired`, and reported on `/health/ha`. Nothing **enforces** it: no
ownership-sensitive operation stamps the token, and no consumer rejects a stale
one. That is safe today because the domain surface is refused while non-owner and
registration is journalled locally, so a stale owner has nothing external to
corrupt. When ownership starts controlling a shared or external resource,
enforcement (stamp the token on the operation, reject anything below the highest
seen) becomes its own plan; the generation is already there to build on.

### Step down to Passive in place

When an Active instance loses or cannot keep its lease, it stops serving and
`Contend` returns; the process leaves and its service manager restarts it. The
draft prefers an in-place transition back to Passive without exiting. The current
choice is the conservative one (exit is unambiguously safe), but it means a
transient renewal failure costs a process restart. Rejoining as Passive needs
`Contend` to loop between the two states rather than run each once.

### Robust CAS for the acquire race

Acquisition writes the successor grant and re-reads to confirm this instance is
the writer that landed, which resolves two instances writing a free lease at the
same instant to one owner. It is not a true compare-and-swap: it leans on the
health gate (a promoter acquires only when the peer is unhealthy, so the two
rarely race) and on the owner stepping down before expiry (so no one renews an
expired grant another instance is taking). A genuine atomic CAS — an OS advisory
lock around the read-modify-write, or a rename-based token — would remove the
residual double-acquire window before the mutex's guarantee is fully matched.
Cover the CAS win/loss paths with tests before relying on it under contention.

### Clock robustness

Expiry is compared against the wall clock. On one host that is skew-free between
the two instances, but a large backward wall-clock step could make a grant look
valid longer or a fresh one look expired. Basing expiry on a monotonic reference
stored alongside the wall time would close it. Called out because the split-brain
safety argument depends on the two instances reading the same clock.

## Testing still owed

- Scenario coverage for failover from a hung-but-alive Active (renewal stopped,
  process alive, `/health` unhealthy), and no-failover from a slow-but-healthy
  Active. Scenarios rebuild the binary at run time, so run them with `-count=1`
  after editing `platform/**`.
- A split-brain assertion: at no observed instant do both instances report
  `runtimeState: active`.
- The frozen-wake self-fence, once fencing enforcement exists to make it
  observable beyond the step-down.
