# Lease-based ownership: current state and what is left

Status: the lease, health-gated promotion, and **automatic failback** described
in [README.md](README.md) are implemented. The Windows named mutex is gone;
Primary Ownership is a renewable lease file authored under a machine's
`platform.standby` block. This document is the authoritative record of what is
done and what remains, so the next pass starts from a written baseline.

## Implemented

- A machine-wide lease file with acquire/renew/release and expiry
  (`platform/internal/redundancy/lease.go`). The owner identity is the instance's
  fixed **role** — a machine's Primary and Standby always differ — which is what
  resolves two instances writing a free lease at the same instant to one owner.
  There is no ownership generation or fencing token.
- Renewal while Active, and self-step-down when the owner cannot renew before its
  lease would lapse (`ownership.go`, `startRenewal`).
- Health-gated promotion: a Standby takes over only when the lease has lapsed
  **and** the peer's `/health` reports it cannot serve. The gate is an
  app-supplied callback, so `redundancy` makes no HTTP call itself
  (`platform/internal/app/lease.go`, `peerHealthCheck`).
- **Automatic failback** (`ownership.go`, `startFailback`): an Active Standby
  whose peer (the Primary) has been continuously healthy for
  `failback_stabilization` steps down, so the preferred Primary reclaims
  ownership. The window resets the moment the Primary looks unhealthy again, so a
  flapping Primary does not trigger a handover. There is no manual mode.
- Real `/health/ha` reporting: `leaseState` and `leaseExpirationUtc` come from
  the live lease.
- Blueprint authoring: `platform.standby.lease { file, duration,
  renewal_interval, health_check_interval, failback_stabilization }`, resolved
  into the descriptor and validated by the builder and the platform.
- Ownership events: `lease_opened`, `ownership_waiting`, `ownership_acquired`,
  `promotion_declined`, `lease_renewal_failed`, `stepped_down`,
  `failback_initiated`, and the activation events.

## Deferred

### Step down / fail back to Passive in place, without a restart

Both self-step-down (a renewal that cannot keep the lease) and failback (a Standby
handing back to the Primary) currently work by the Active instance **stopping and
exiting**: `Contend` returns, the process leaves, and its service manager restarts
it Passive. The Primary then reclaims ownership through the existing health gate,
because the exited Standby is no longer answering its health endpoint.

This is the conservative choice — exit is unambiguously safe — but it has two
costs:

- It relies on a service manager to bring the instance back. In an environment
  with none (a bare dev run, some scenario harnesses), a Standby that fails back
  exits and does not return, leaving no standby until it is restarted.
- Every failback and every transient step-down is a process restart.

Rejoining as Passive **in place** would remove both. It is a single piece of work
that both cases share, and it is the reason they are grouped here. It needs two
changes:

- `Contend` must loop between Passive and Active over the instance's lifetime,
  rather than running each once. It would distinguish "the process is stopping"
  (return) from "handed back or lost the lease" (go Passive again) — the failback
  path already has the signal, since it cancels a child context while the process
  context stays live.
- The app's Active teardown must stop shutting the listener down on the way out.
  Today `runActive` calls `server.shutdown` on return, which is correct for a
  process stop but closes the listener a returning-to-Passive instance needs to
  keep. It would instead drain the in-flight active requests, swap back to the
  Passive handler (`instanceServer.serveWith`), and leave the listener open. The
  Passive callback already re-selects the Passive handler; the missing piece is a
  drain-without-close on `instanceServer`.

Once this lands, failback keeps the Standby alive as Passive, and the
Preferred-Primary handover costs no restart.

### Robust CAS for the acquire race

Acquisition writes the successor grant and re-reads to confirm this instance's
role is the one that landed, which resolves two instances writing a free lease at
the same instant to one owner. It is not a true compare-and-swap: it leans on the
health gate (a promoter acquires only when the peer is unhealthy, so the two
rarely race) and on the owner stepping down before expiry (so no one renews an
expired grant another instance is taking). A genuine atomic CAS — an OS advisory
lock around the read-modify-write, or a rename-based token — would remove the
residual double-acquire window. Cover the CAS win/loss paths with tests before
relying on it under contention.

### Clock robustness

Expiry is compared against the wall clock. On one host that is skew-free between
the two instances, but a large backward wall-clock step could make a grant look
valid longer or a fresh one look expired. Basing expiry on a monotonic reference
stored alongside the wall time would close it. Called out because the split-brain
safety argument depends on the two instances reading the same clock.

## Testing still owed

- Scenario coverage for failover from a hung-but-alive Active (renewal stopped,
  process alive, `/health` unhealthy), no-failover from a slow-but-healthy Active,
  and automatic failback (Standby Active, Primary returns healthy, Primary ends up
  Active). Scenarios rebuild the binary at run time, so run them with `-count=1`
  after editing `platform/**`.
- A split-brain assertion: at no observed instant do both instances report
  `runtimeState: active`.
