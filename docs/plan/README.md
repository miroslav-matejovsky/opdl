# Local warm standby redundancy

Status: Proposed for the POC.

This plan adds two independent platform processes on one machine. One process is
active. The other keeps a warm local projection and can take over after the
active process exits. The design preserves the event-driven architecture: NATS
JetStream remains the source of truth, and a local operating-system lock is used
only to fence active process capabilities on one machine.

Backward compatibility is out of scope. The unused project-level
`features.redundancy` switch will be replaced rather than preserved.

## Objectives

- Enable one warm standby by default for every machine in its resolved
  deployment descriptor.
- Allow `warm_standby = false` in an individual blueprint machine's `platform`
  subsection.
- Run the active and standby as separate OS processes using the same packaged
  binary and descriptor.
- Guarantee that only one process on a machine serves the public API, runs
  decision handlers, or owns that machine's embedded NATS server and storage.
- Keep the standby caught up from the retained site journal without producing
  domain decisions.
- Promote the standby automatically after the active process exits and releases
  its OS lock.
- Support a controlled upgrade by starting a new standby, verifying catch-up,
  and gracefully stopping the old active.
- Preserve machine-scoped registration decisions. Instance identity is for
  lifecycle and diagnostics, not a second domain voter.

## Terms

| Term | Meaning |
| --- | --- |
| Machine | The existing compiled deployment identity and one registration voter. |
| Slot | Stable local process identity `a` or `b`. It does not change when active ownership changes. |
| Active | The slot holding the machine's exclusive active fence. It owns externally visible and decision-producing capabilities. |
| Warm standby | A slot connected to the journal with caught-up local projections, but no public listener, durable domain handlers, or embedded server. |
| Fence | An exclusive OS file lock shared only by the two processes on one machine. Process exit releases it automatically. |
| Promotion | Recomposition of a fenced standby into the active runtime. |
| Handover | Graceful stop of the active after a caught-up standby is ready to promote. |

## Core decisions

- Use two OS processes, not two goroutines. A process crash must not remove both
  copies.
- Use symmetric slots `a` and `b`. The first healthy slot to acquire the fence
  is active; there is no permanently preferred primary.
- Use a non-expiring OS lock. A paused or unhealthy process must be terminated by
  its service manager before another slot can become active. A timeout lease
  would allow the old process to wake and create split brain.
- Store the lock and local status under a machine-specific local runtime
  directory shared by both slots. The directory must be on a local filesystem.
- Keep the same machine identity for proposal and decision IDs. Add slot identity
  only to operational lifecycle identity and logs.
- A standby on a storage machine connects as a NATS client to the active local
  server or another storage node. It never opens the shared JetStream data
  directory. After promotion it closes the client-only composition, opens the
  embedded server under the fence, and catches up again before serving.
- A standby runs projectors only. It does not attach the machine's durable
  handlers, publish readiness, or bind the public API.
- Planned handover may contain a short listener transfer gap. The POC promises
  no lost accepted command and bounded failover, not uninterrupted TCP
  acceptance. Strict zero-downtime listener handoff needs a separate front-door
  design and is not hidden inside this work.
- Full machine shutdown stops the standby before the active. Otherwise the
  standby would correctly interpret active shutdown as a reason to promote.

## Descriptor and authoring shape

Warm standby is a machine policy, authored under a `platform` subsection of the
machine so a blueprint reader sees platform-runtime policy grouped and explicit.
An omitted `platform` block, or an omitted `warm_standby` inside it, means
enabled:

```hcl
machine "sensor" {
  role     = "sensor-node"
  ip       = "10.0.1.10"
  services = ["sensor-services"]

  platform {
    warm_standby = false # optional opt-out
  }
}
```

Every resolved descriptor states the result explicitly:

```json
{
  "instances": {
    "warm_standby": true
  }
}
```

The project-level `features.redundancy` field is removed. `features.chaos`
remains project-level.

## Stages

Estimates are for one engineer and include code, tests, and documentation.

| Stage | Outcome | Complexity | Estimate | Status |
| --- | --- | --- | --- | --- |
| [1. Instance and fencing contract](01-instance-contract.md) | Freeze identities, states, ownership, and failure rules | Medium | 2-3 days | Not started |
| [2. Descriptor and package contract](02-descriptor-and-package.md) | Make standby default-on and machine-specific | Medium | 3-5 days | Not started |
| [3. Warm standby runtime](03-warm-standby-runtime.md) | Run a caught-up projection-only second process under local fencing | High | 5-8 days | Not started |
| [4. Promotion and handover](04-promotion-and-handover.md) | Promote safely after crash or controlled active shutdown | High | 4-7 days | Not started |
| [5. Resilience validation and cleanup](05-validation-and-cleanup.md) | Prove failover, opt-out, recovery, and remove the legacy flag | High | 3-5 days | Not started |

Total estimate: 17-28 engineering days.

## Completion criteria

- A descriptor without an override explicitly enables warm standby.
- A machine-level `warm_standby = false` produces a single-slot package.
- Two independently launched processes never hold active capabilities together.
- Killing the active process promotes a caught-up standby without losing retained
  registrations or producing a second machine decision.
- A storage-machine standby never opens the active server's data directory until
  it owns the fence.
- A controlled handover drains accepted work, promotes the standby, and restores
  service on the same public address.
- Live projection lag prevents activation and removes a lagging active from
  service.
- Full machine shutdown does not accidentally promote the standby.
- `task all` passes.

## Non-goals

- Active-active service execution.
- Failover to another physical machine.
- A distributed election or shared state store.
- Automatic preemption of a live process that still owns the OS fence.
- Compatibility with descriptors or journals produced before this POC change.
