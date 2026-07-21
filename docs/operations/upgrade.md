# Switchover and upgrade

Two procedures: moving active ownership between a machine's two processes on
purpose, and upgrading a machine's binary using that move.

Both rest on one invariant. A process releases the machine fence only after its
active resources have closed, so the process that next acquires it binds the same
endpoints with no overlap. Nothing here steals ownership; the platform has no
mechanism to.

## Controlled switchover

Moves active ownership from the current holder to the machine's other process.

### Preconditions

Check all of them. Each is machine-readable from the target's status file.

| Condition | Where |
| --- | --- |
| Target is `state=standby` | status file |
| Target reports `promotable=true` | status file |
| Target has no `last_error` | status file |
| Target's `updated_at` is advancing | status file, written every second |
| Target's PID is live | status file plus the service manager |
| Both processes report the same `fence.object` | `platform.fence_opened` |

A stale status file is the trap. Its `updated_at` stops advancing when the
process dies, but the file remains, so a PID check alone is not enough.

### Procedure

1. Verify the preconditions above.
2. Stop the holding process through the service manager. It stops HTTP intake,
   drains in-flight requests, stops handlers, states
   `platform.event_fabric.stopping`, stops its projector, closes its embedded
   NATS server, and only then releases the fence.
3. The standby's kernel wait returns immediately. It activates.
4. Wait for the promoted process to report `state=active` with
   `applied >= high_water`.
5. Start the stopped process again if the machine should keep a warm standby. It
   finds the fence held and waits.

Expect `platform.fence_acquired` with `abandoned=false`. A planned switchover that
reports `abandoned=true` means the holder died rather than released, and step 2
did not do what it appeared to.

### Reclaiming the preferred primary

Run the same procedure in the opposite direction. A returning primary never steals
ownership: it starts, finds the fence held, and waits as a standby until a
switchover hands it over.

## Rolling upgrade

Upgrades a machine's binary with interruption bounded by one switchover.

### Per machine, standby enabled

1. Verify the machine has a healthy holder and a promotable standby.
2. Stop the standby service, replace its package, start it. It rejoins as a
   waiter and warms its projection.
3. Wait for it to report `state=standby`, `promotable=true`, no `last_error`.
4. Perform a controlled switchover. The upgraded process takes ownership.
5. Stop the now-standby former holder, replace its package, start it.
6. Optionally switch back so the preferred primary holds again.

Both processes must run the same machine package before the upgrade is complete.
They share one set of endpoints and one fence object, and a package built from a
different blueprint may derive a different `fence.object`, which would leave them
failing to exclude each other. Confirm `platform.fence_opened` reports the same
object from both after step 5.

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
produces an abandoned fence rather than a clean release. Both are safe, and only
the first is a planned switchover. Closing that gap is tracked in
`docs/plans/fence-ownership/`.
