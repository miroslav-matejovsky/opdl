# Stage 03: Named Pipe event channel

**Effort:** Medium. **Complexity:** High. **Risk:** Medium.

Depends on stages 01 and 02. Introduces the first local IPC mechanism in the
repository and the first push channel of any kind.

## What the pipe is for, and what it is not

The pipe delivers leadership notifications from the platform to the local units:
`LeadershipGranted` and `LeadershipRevoked`, and the resulting `Active` or
`Standby` state.

**The pipe is not part of the safety argument.** It is tempting to make it one,
because it looks like the natural place to enforce "revoke the old Active before
granting the new one". It cannot carry that weight: a pipe cannot deliver to a
process that is paused, wedged, or gone, which is exactly the case where the old
Active must stop.

Safety comes from stage 01: self-demotion strictly before possible expiry, and the
epoch as a fencing token. The pipe makes a unit learn about a change in
milliseconds instead of at its next poll. That is a large operational improvement
and no correctness contribution at all.

Stating this plainly is the point of this section. If a later change ever makes
correctness depend on pipe delivery, the design has regressed.

## Delivery contract

The channel is lossy by assumption and safe by construction.

1. On connect, the platform sends a **snapshot**: current state and epoch for
   every unit type the connecting unit cares about. Not a delta.
2. After the snapshot, it sends **deltas** as leadership changes.
3. Every message carries its epoch.
4. A unit discards any message whose epoch is not greater than the highest it has
   seen. Duplicates and reordering become harmless.
5. On disconnect, the unit reconnects and gets a fresh snapshot. It does not
   attempt to resume a stream or replay missed messages.
6. A unit that has been disconnected treats its state as unknown rather than as
   its last known value, and resolves it through the local API from stage 02
   before acting as Active.

Rule 6 is the one that is easy to get wrong. A unit that keeps behaving as Active
through a disconnection is a unit whose Active status is being decided by a
channel that is down.

## Backpressure

A slow or stopped unit must never block the platform.

- One bounded queue per connected client.
- On overflow, drop the queued deltas and enqueue a single resync marker.
- The unit responds to a resync marker by querying the local API.

Dropping deltas is safe precisely because the API is authoritative and the epoch
makes stale messages identifiable. A design that instead blocked the publisher, or
grew the queue without bound, would let one wedged client service degrade the
platform.

## Windows specifics

| Concern | Decision |
| --- | --- |
| Pipe name | Derived from deployment identity: project, environment, site, machine. Not configured. Discoverable without out-of-band configuration, which the HTTP base URL is not. |
| Mode | Message mode, so a read returns one whole notification and no framing protocol is needed. |
| Remote clients | `PIPE_REJECT_REMOTE_CLIENTS`. This is a local channel. |
| Instances | One server instance per connected client, with a bounded maximum. |
| Security | Explicit DACL granting the accounts the platform's units run under. Do not accept the default and do not grant world access. |
| Impersonation | The server must not impersonate clients. |

Pipe name derivation should be settled together with stage 02's discovery
discussion, since the pipe name is the one identifier a unit can compute rather
than be told.

## Lifetime

The pipe server has the same lifetime as the public HTTP listener: it belongs to
the platform process holding the machine fence, and a promoted process reopens it.
The measured promotion window on the development baseline is roughly 130 to 190 ms
(`docs/01-architecture.md:367`).

Units therefore see the pipe drop during platform failover. This is routine, not
exceptional, and the reconnect path in the delivery contract handles it. It is
worth noting that the platform's own failover briefly makes every local unit
uncertain about its role, which is correct: during that window the machine has no
process that can speak for it.

## Authentication and authorization

There is none today. The HTTP API is anonymous
(`sdk-dotnet/README.md:104`) and `docs/02-registration.md:162` lists
authentication and authorization as outside the implementation. The root `.todo`
carries both as open platform features.

The pipe does not close that gap, and this stage should not pretend otherwise.
What it does provide is that the DACL restricts which local accounts can connect
at all, which is a stronger position than the anonymous TCP listener. Any claim
beyond that belongs to the platform-wide authentication work, not here.

## Work

1. New package for the pipe server, owned by the platform, with the transport
   confined to it in the same way NATS is confined to `eventfabric/nats`.
2. Snapshot and delta message types, versioned, self-describing, epoch-carrying.
3. Per-client connection handling: bounded queue, overflow to resync, clean
   teardown.
4. Composition into the active runtime alongside the HTTP listener, with the same
   start and stop ordering.
5. Windows security descriptor construction, with the accounts configurable.

## Tests

- A connecting client receives a snapshot before any delta.
- A delta with a lower epoch than the client's last is discarded by the client.
- A stopped reader does not block the publisher, and receives a resync marker.
- Server shutdown closes connections cleanly and clients reconnect.
- Remote connection attempts are rejected.
- Killing the active platform process drops the pipe, and the promoted process
  serves it again on the same name.

## Non-Windows

The repository builds and tests on both Windows and Linux, and `utils/filelock`
already carries `lock_windows.go` and `lock_unix.go`. The pipe channel needs the
same treatment: a platform-neutral interface with a Windows named-pipe
implementation and a unix-domain-socket implementation, so scenarios and CI keep
running on Linux. Deciding to be Windows-only would take Linux CI coverage away
from this feature, which is the coverage most likely to catch a logic error in the
delivery contract.
