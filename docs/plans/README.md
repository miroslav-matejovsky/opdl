# Local redundancy: finalization plan

Status: proposed. This plan supersedes the earlier lease-design documents and the
`health.md` / `redundancy.md` drafts, all removed; what they decided that still
matters is restated here or already lives in the code
(`platform/internal/redundancy`, `platform/internal/app`, `platform/internal/httpapi`).

## Goal

Bring the implemented local redundancy to an acceptable, finished level. The
contract, in full:

- A machine runs two fixed-role instances, Primary and Standby, always on the
  same host. This is **local redundancy only** — nothing here coordinates across
  machines.
- Exactly one instance is Active at a time, decided by a renewable lease file.
  Switching between Active and Passive is **automatic only**; there is no manual
  mode and none will be added.
- The **Primary is always preferred**. If it fails, the Standby takes over
  (lease lapsed **and** peer unhealthy). When the Primary returns, it takes
  ownership back — but only after a continuous-health **stabilization period**,
  so a flapping Primary never causes ping-pong.
- **Passive does not mean the HTTP server is off.** A Passive instance keeps its
  listener bound and serves the health endpoints and `/instance`; it refuses
  every other operation and accepts no writes. This read-only surface is made
  explicit on the HTTP boundary rather than implied.
- Keep it simple. No fencing tokens, no distributed concerns, no configuration
  beyond the existing `platform.standby.lease` blueprint block.

## Where the implementation stands

Already working (see `platform/internal/redundancy`):

- Lease file with acquire/renew/release, expiry, and role-based owner identity.
- Health-gated promotion and self-step-down before the lease could lapse.
- Automatic failback with a stabilization window that resets on a flap.
- Passive HTTP surface serving health + `/instance` and refusing domain
  operations with a 503 that names where to go.
- Real `/health`, `/health/live`, `/health/ready`, `/health/ha` data.
- Blueprint `platform.standby.lease { file, duration, renewal_interval,
  health_check_interval, failback_stabilization }` resolved into the descriptor.

The gaps this plan closes, in order:

| Step | What | Complexity | Effort | Status |
| --- | --- | --- | --- | --- |
| [01](01-explicit-serving-mode.md) | Explicit serving mode on the HTTP boundary | Low | S (~½ day) | done |
| [02](02-in-place-transitions.md) | In-place Passive↔Active transitions — no process exit on step-down or failback | High | L (2–3 days) | done |
| [03](03-integration-tests.md) | Integration tests in the redundancy package | Medium | M (~1 day) | open |
| [04](04-scenarios.md) | Failover and failback scenarios in the scenarios module | Medium | M (~1 day) | open |
| [05](05-cleanup.md) | Documentation and code alignment, close-out | Low | S (~½ day) | open |

Steps 01 and 02 change behavior; 03 and 04 prove it; 05 tidies. 01 can land
independently. 03 depends on 02 (it asserts in-place cycling). 04 depends on 02
(a failback scenario without a service manager only works when the Standby
survives its own step-down).

## Accepted limitations (deliberately out of scope)

Named so nobody reopens them by accident:

- **Acquire race hardening.** Acquisition is write-then-confirm rather than a
  true CAS. The health gate and pre-expiry step-down make the race practically
  unreachable on one host; a kernel-level CAS is not worth its complexity here.
- **Clock steps.** Expiry uses the wall clock. Both instances read the same
  host clock, which is the safety argument; a large backward step during a
  failover window is accepted risk.
- **Fencing tokens / ownership generation.** Removed earlier and staying
  removed. Nothing downstream consumes one.
- **Manual failover or failback.** Automatic only.
