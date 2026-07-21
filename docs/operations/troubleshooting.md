# Troubleshooting

Start every investigation with four facts: the expected machine and role, the
latest status file, the process stderr log, and all PID-specific operational
JSONL files for the incident window. Merge site files by timestamp when more than
one machine is involved.

## Process does not start

1. Find `platform.process_stopped` and its `attributes.error`.
2. Confirm the package identity in `manifest.json` and `deployment.json` matches
   the host.
3. Check TOML syntax and unknown-key errors. Remove old NATS socket overrides,
   and any leftover `address` or `instance_dir`: both are an instance's own now,
   both come from the descriptor, and a file that sets either fails to load.
4. Verify this instance's `runtime_dir`, and the journal data, credentials, and
   operations directories, are writable by the service identity. Read the exact
   runtime directory from the instance's record in `deployment.json`.
5. Check the resolved NATS addresses are assigned to the host and not already
   bound. The API address is on `127.0.0.1` by construction, so only its port can
   conflict, and only with something else on loopback.

If no operational event exists, configuration loading or role validation failed
before the recorder opened. Use the plain stderr error.

## Journal startup times out

Look for `event_fabric.journal_retry`:

- `context deadline exceeded` or no responders during a three-node cold start
  usually means fewer than two storage nodes are reachable or routes/firewalls
  are wrong;
- error 10005, no suitable peers, means the metadata leader cannot see all
  required storage peers yet;
- error 10059, stream not found, can occur while a local server converges after
  creation and is retried within `startup_timeout`;
- incompatible journal errors are not transient. Compare the stored journal
  configuration with the packaged replica and limit contract before recovery.

Start all three storage machines together on a cold replicated site. Confirm
their cluster ports are mutually reachable. Do not delete journal storage to
clear a timeout; that storage is the durable platform state.

## API stops after a storage-node failure

1. Check `event_fabric.client_disconnected` and the last selected server.
2. Expect `event_fabric.client_reconnected` to another storage node.
3. Expect `event_fabric.projector_reset` and `event_fabric.handler_reset`, then
   check for attachment retry events.
4. Check for error-level `projector_stopped`, `handler_stopped`, or
   `platform.background_loop_stopped`.
5. Read status `last_error`, `lag`, `applied`, and `high_water`.
6. Verify at least two of three storage nodes and their cluster links remain.

Storage processes connect to their own embedded server. Client-only processes
use the stable sorted storage list and reconnect every 250 ms. If connection
returns but projection does not advance, retain all JSONL and journal data and
escalate as a consumer-resume defect. Do not restart every storage node at once.

`event_fabric.consumer_heartbeat_missed` is a degradation signal, not a stopped
consumer. The NATS client has already issued another pull and the platform keeps
the iterator alive. Investigate repeated events together with growing projection
lag. A `handler_stopped` or `projector_stopped` event whose error is only `no
heartbeat received` identifies an older platform binary without this recovery.

## Writes fail briefly after one storage node is killed

A three-replica journal must elect a new leader. Writes submitted during that
window can fail because the platform does not currently retry publishes
internally. Retry the client request with a bounded backoff and stable request
identity. This remains an explicit product decision in
`docs/backlog/event-fabric.md`.

Persistent failure is not the expected election window. Verify two storage nodes
are alive, routes are connected, disk is writable, and no background loop has
failed.

## Projection lag grows

1. Calculate `high_water - applied` from successive status files.
2. Check client disconnect and asynchronous error events.
3. Check handler stop events and retained-work catch-up durations.
4. Check CPU, memory, and disk latency on the affected machine.
5. Compare other machines in the site to separate a local consumer problem from
   journal-wide load.

An active process stops serving when continuous lag exceeds `lag_bound`. Do not
increase the bound only to suppress the symptom. Increasing it explicitly allows
older query results for longer and changes the site's safety posture.

## Standby does not become failover-ready

The standby should emit `platform.standby_ready` and
`platform.standby_waiting`. Its status must be fresh, `state=passive`,
`failover_ready=true`, and have no `last_error`.

- Confirm the active process owns and serves the machine's one client port.
- Confirm primary and standby use the same executable and TOML file.
- Confirm the standby binds no API, NATS listener, or journal storage.
- Inspect connection and projection catch-up events.
- Confirm both processes were built from the same machine package, so they carry
  the same `lock.windows_mutex`.

Never start a standby with a separate client port. Both roles share one
machine-level Event Fabric endpoint and ownership controls who binds it.

## The standby service is the one serving

Expected after a failover, and not a fault. The instance role is fixed; the state
is not. A Standby Instance reporting `state=active` owns the machine and serves
correctly, and the Primary Instance beside it reports `state=passive`.

Ownership does not return on its own. It moves back only when the Active instance
is stopped, which is the controlled Ownership Transfer in `upgrade.md`. Until then
this is a steady state, so alert on how long it lasts rather than on the fact of
it. `monitoring.md` tabulates the role and state combinations.

Investigate only if the Primary Instance is not `failover_ready=true` with a fresh
status, because that is what a transfer needs and it is the part that can be
broken.

## Two processes appear active

Status files are not ownership evidence. Start from the operational events, not
from the filesystem: ownership is a kernel object and has no path.

- `platform.lock_opened` reports the mutex each process opened. Both processes
  of a machine must report the same one. Two different mutexes means they were
  built from different packages, or from blueprints with different
  `lock.windows_mutex` values.
- `platform.ownership_acquired` reports which process took it, and whether it was
  `abandoned`. An abandoned acquisition means the previous holder died rather than
  handed over.
- The startup summary prints the ownership object alongside the rest of the
  descriptor.

To confirm from outside the platform, list handles to the object with Process
Explorer or `handle.exe` filtered on the object name. Neither ships with Windows,
so prefer the events above. Note that a machine whose processes are all stopped
leaves no object behind: it exists only while a process holds it open.

Stop both services, correct the directory placement and service configuration,
then start the preferred primary before the standby. Preserve status and event
files for analysis.

## Operational JSONL is missing or cannot be written

Structured events should still be present on stderr. At startup, an unusable
configured event directory is fatal. A later write failure emits fallback text:

```text
platform operations JSONL write failed: path=... error=...
```

Check directory ownership, free space, filesystem health, and external rotation.
The service manager's stderr capture is the recovery source. Fix the sink and
restart the process to create a new PID-specific file.

## Port exposure is larger than expected

Expected listeners are the public API, the NATS client port on storage machines,
and the cluster port only on three-node storage topologies. There is no NATS
monitor listener.

If another port is open, identify the owning PID. Confirm the package is current
and runtime TOML contains no obsolete settings. Do not expose a raw NATS monitor
to obtain health data; use status and operational events.

## Safe evidence collection

Collect without modifying state:

- package `manifest.json`, `release.json`, and checksums;
- redacted runtime TOML and the credentials file path, never its content;
- primary and standby status files;
- service-manager stdout and stderr;
- all relevant operational JSONL files;
- listener and process inventory;
- disk capacity and filesystem errors for journal and operations paths.

Do not attach the journal directory casually. It contains the durable event
history and may contain customer data. Follow the site's data-handling policy.
