# Switchover and upgrade

Two procedures: moving active ownership between a machine's two processes on
purpose, and upgrading a machine's binary using that move.

Both rest on one invariant. A process releases Primary Ownership only after its
active resources have closed, so the process that next acquires it binds the same
endpoints with no overlap. Nothing here seizes ownership; the platform has no
mechanism to.

> [!WARNING]
> **Full restart required when migrating from legacy fence nomenclature to lock nomenclature.**
> Upgrading across this change requires a full restart of every machine that deploys a standby instance. Because the mutex name format changed and is now authored in full rather than derived (`opdl.fence.<digest>`), an old primary and a new standby would contend for different named mutexes. Therefore, both existing processes on the machine must be stopped before starting the upgraded primary or standby.

## Controlled switchover

Moves active ownership from the current holder to the machine's other process.

### Preconditions

Check all of them. Each is machine-readable from the target's status file.

| Condition | Where |
| --- | --- |
| Target is `state=passive` | status file |
| Target reports `failover_ready=true` | status file |
| Target has no `last_error` | status file |
| Target's `updated_at` is advancing | status file, written every second |
| Target's PID is live | status file plus the service manager |
| Both processes report the same `lock.windows_mutex` | `platform.lock_opened` |

A stale status file is the trap. Its `updated_at` stops advancing when the
process dies, but the file remains, so a PID check alone is not enough.

### Procedure

1. Verify the preconditions above.
2. Stop the holding process through the service manager. It stops HTTP intake,
   drains in-flight requests, stops handlers, states
   `platform.event_fabric.stopping`, stops its projector, closes its embedded
   NATS server, and only then releases ownership.
3. The standby's kernel wait returns immediately. It activates.
4. Wait for the newly Active instance to report `state=active` with
   `applied >= high_water`.
5. Start the stopped process again if the machine should keep a warm standby. It
   finds ownership held and waits.

Expect `platform.ownership_acquired` with `abandoned=false`. A planned switchover that
reports `abandoned=true` means the holder died rather than released, and step 2
did not do what it appeared to.

### Failback

Failback is returning ownership to the Primary Instance after a failover. It is
the same procedure run in the opposite direction, and **its trigger is an
operator**: the platform never fails back on its own, and there is no failback
policy to configure.

A returning Primary Instance does not seize ownership. It starts, finds ownership
held, and waits in the Passive state, reporting `role=primary, state=passive` until
someone stops the Standby Instance that is currently Active. That combination is a steady state, not a
transient one.

## Rolling upgrade

Upgrades a machine's binary with interruption bounded by one switchover.

### Per machine, standby enabled

1. Verify the machine has a healthy holder and a failover-ready standby.
2. Stop the standby service, replace its package, start it. It rejoins as a
   waiter and warms its projection.
3. Wait for it to report `state=passive`, `failover_ready=true`, no `last_error`.
4. Perform a controlled switchover. The upgraded process takes ownership.
5. Stop the now-standby former holder, replace its package, start it.
6. Optionally switch back so the preferred primary holds again.

Both processes must run the same machine package before the upgrade is complete.
They share one set of endpoints and one ownership lock, and a package built from a
different blueprint may author a different `lock.windows_mutex`, which would leave them
failing to exclude each other. Confirm `platform.lock_opened` reports the same
mutex from both after step 5.

### Machines without a standby

A machine with `standby.disabled = true` cannot roll. One process cannot hand over
to itself, so the upgrade is stop, replace, start, and the machine is down for the
duration. This is a property of the deployment decision, not a limitation of the
procedure. Plan the outage rather than discovering it in a maintenance window.

### Site ordering

Machines are upgraded one at a time, and storage machines need more care than the
rest.

The site's journal is replicated across its storage machines: one for a site
smaller than three machines, the first three by sorted name otherwise. A
three-storage-node site tolerates losing one. Upgrading two at once loses quorum
and the site stops accepting writes until a replica returns.

1. Upgrade non-storage machines first, in any order.
2. Upgrade storage machines strictly one at a time.
3. Between storage machines, wait for the journal to accept writes again and for
   every node's projection to be caught up.

A one- or two-machine site has a single storage node and no journal-node failure
tolerance, so upgrading it stops the site's writes for the duration whatever the
standby policy says.

## What is not covered

The platform has no Service Control Manager integration. It handles
`os.Interrupt`, which the runtime raises for `CTRL_C_EVENT` and
`CTRL_BREAK_EVENT`, and a service manager that terminates the process instead
produces abandoned ownership rather than a clean release. Both are safe, and only
the first is a planned switchover. Closing that gap is tracked in
`docs/plans/redundancy/`.
