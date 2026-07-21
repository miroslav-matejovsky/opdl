# Monitoring and event analysis

## Asking an instance what it is

Every instance answers `GET /instance` on its own loopback address, in every
state, for its whole lifetime:

```json
{
  "machine": "local-server",
  "role": "primary",
  "state": "active",
  "address": "127.0.0.1:8080",
  "peer_address": "127.0.0.1:8081"
}
```

`role` is fixed at build time and never changes. `state` is `active` or `passive`
and changes only when Primary Ownership moves, so asking both of a machine's
addresses is how an operator answers "which one is serving" without reading a
file or a log.

A Passive instance answers this and refuses every domain operation with `503` and
a body naming `peer_address`, which is where ownership is. A `503` from this
platform therefore means "you reached the wrong instance", not "the platform is
down" — the address in the body is the one to use.

Nothing in this endpoint comes from the site journal, so it answers while an
instance's projection is still catching up, and while it never does. That is what
makes it usable when something is wrong.

## Status files

Each instance writes one status file inside its own runtime directory:

```text
<runtime_dir>/process.status
```

`runtime_dir` is authored per instance in the project blueprint and resolved onto
that instance's descriptor record, so a machine's two instances write two files in
two directories. The path carries no project, machine, or role qualifier: those
are in the authored directory itself. Read `deployment.json` to see the exact path
each instance was built with.

They are atomic JSON snapshots with these fields:

| Field | Meaning |
| --- | --- |
| `role` | Which fixed instance wrote this: `primary` or `standby` |
| `state` | What that instance is doing: `active` or `passive`, or a transitional `starting`, `activating`, `stopping`, `failed` |
| `pid` | Process that wrote the snapshot; validate it is still alive |
| `applied` | Highest journal sequence projected locally |
| `high_water` | Highest accepted journal sequence last observed |
| `lag` | Continuous time behind the journal |
| `failover_ready` | Whether this instance is current enough to take over |
| `updated_at` | UTC snapshot time |
| `last_error` | Most recent health or lifecycle error |

Primary Ownership, not the status file, decides which instance is Active. A
stale file after a crash is historical evidence. Treat it as live only when
`updated_at` is fresh and `pid` belongs to the expected service.

### Role and state are independent axes

`role` (`primary` or `standby`) is fixed for the life of the installation. `state`
changes (`active` vs `passive`). Both axes must be read together:

| `role` | `state` | Meaning |
| --- | --- | --- |
| `primary` | `active` | Normal operation |
| `standby` | `passive` | Normal operation |
| `standby` | `active` | **Failover has occurred.** The machine is serving from its Standby Instance |
| `primary` | `passive` | The Primary Instance is available (`role=primary, state=passive`) but does not own. Ownership returns only when the Active instance is stopped, so this persists until an operator acts |
| both `active` | | Must never occur, and cannot |

The fourth row is not an error and not transient. It is the steady state after a
returning Primary Instance rejoins, and it stays until a controlled Ownership
Transfer moves ownership back. See `upgrade.md`.

## Operational event stream

Every event is one JSON object on one line. It is always written to stderr. When
`operations.event_dir` is set, the same line is appended to:

```text
<event_dir>/<project>-<environment>-<site>-<machine>-<role>-<pid>.jsonl
```

Files are mode 0600 and PID-specific. Restarts create new files. The platform
does not rotate or delete them. Apply a retention policy only to files whose PID
is no longer running. On Windows, stop the process before moving or deleting its
open JSONL file.

Common fields are `timestamp`, `type`, `level`, `component`, deployment identity,
`role`, `pid`, `message`, and event-specific `attributes`. Attribute values never
contain credentials.

Raw embedded NATS server logs are disabled. The platform records the server,
connection, journal, consumer, and readiness outcomes it can act on. This avoids
retaining a second transport-specific log stream or enabling another listener.

Important event types:

| Type | Operational meaning |
| --- | --- |
| `platform.process_started`, `platform.process_stopped` | Process lifetime and terminal error |
| `platform.lock_opened`, `platform.ownership_acquired`, `platform.ownership_waiting` | Active ownership transition. `lock_opened` names the kernel object, which has no path; `ownership_acquired` carries `abandoned`, distinguishing a crash failover from a planned handover |
| `platform.activation_started`, `completed`, `failed` | Initial activation, failover, or failback with duration |
| `event_fabric.server_starting`, `server_ready` | Embedded storage server lifecycle |
| `event_fabric.client_connected`, `disconnected`, `reconnected`, `closed` | Selected NATS server and connection transitions |
| `event_fabric.client_connect_retry`, `client_async_error` | Connection degradation |
| `event_fabric.journal_retry`, `journal_recovered`, `journal_ready` | Cluster formation and journal availability |
| `event_fabric.projector_started`, `projector_stopped` | Ordered projection loop health |
| `event_fabric.handler_started`, `handler_stopped` | Durable handler loop health |
| `event_fabric.projector_reset`, `handler_reset` | Consumer recreation after connection or leadership change |
| `event_fabric.projector_attach_retry`, `handler_attach_retry` | Transient or ambiguous consumer attachment |
| `event_fabric.consumer_heartbeat_missed` | Pull delivery missed an idle heartbeat and automatically continued |
| `platform.projection_caught_up` | Catch-up phase, high-water mark, applied sequence, duration |
| `platform.site_ready`, `platform.standby_ready` | Composition readiness |
| `platform.background_loop_stopped` | A projector or handler ended; error level means serving will stop |
| `platform.projection_lag_exceeded` | Active serving safety bound crossed |
| `platform.api_listening`, `platform.api_stopped` | Public API availability |

## Recommended metrics

No Prometheus endpoint is exposed yet. Derive these metrics in the log pipeline
or a local collector:

| Metric | Source | Interpretation |
| --- | --- | --- |
| Process availability | Fresh status plus live PID | Service is running and updating health |
| API availability | Active status plus HTTP probe | Active capability is reachable |
| Projection backlog | `high_water - applied` | Number of accepted events not projected locally |
| Projection lag seconds | Parse status `lag` | Continuous duration behind, used for serving safety |
| Promotable standby count | Standby statuses | Local failover readiness |
| Activation duration | `platform.activation_completed.attributes.duration_ms` | Initial activation, failover, and failback performance |
| Catch-up duration | `platform.projection_caught_up.attributes.duration_ms` | Replay and post-handler convergence performance |
| Connection outage duration | Time from `client_disconnected` to `client_reconnected` | Transport recovery performance |
| Journal startup attempts | `journal_retry` and `journal_recovered` | Cluster formation delay or instability |
| Background loop failures | Error-level `background_loop_stopped` | Projection or handler correctness failure |
| Write availability | Registration POST result codes | Includes the brief leader-election rejection window |

Do not state an availability or failover SLO from development samples. Establish
percentiles across multiple production-like Windows hosts first.

## Recommended alerts

Critical:

- active status is missing or older than 5 seconds while the service should run;
- status is `failed`, has a non-empty `last_error`, or the PID is not live;
- active `lag` reaches `lag_bound` or `platform.projection_lag_exceeded` appears;
- `platform.site_open_failed`, `platform.activation_failed`, or an error-level
  `platform.background_loop_stopped` appears;
- no active API is reachable for a machine;
- fewer than two of three storage machines are available.

Warning:

- `client_disconnected`, `client_connect_retry`, or `journal_retry` appears;
- `high_water - applied` grows across successive status samples;
- a configured standby is not fresh and failover-ready;
- JSONL sink fallback text appears on stderr;
- disk use in the journal or operations directory crosses the site's capacity
  threshold.

## Query examples

With `jq`:

```sh
jq -c 'select(.level == "error")' /var/log/opdl/events/*.jsonl
jq -c 'select(.type | startswith("event_fabric.client_"))' /var/log/opdl/events/*.jsonl
jq -r 'select(.type == "platform.activation_completed") | [.timestamp,.machine,.role,.attributes.activation_kind,.attributes.duration_ms] | @tsv' /var/log/opdl/events/*.jsonl
```

With PowerShell:

```powershell
Get-ChildItem C:\ProgramData\opdl\events\*.jsonl |
  Get-Content |
  ForEach-Object { $_ | ConvertFrom-Json } |
  Where-Object level -eq 'error'
```

For an incident timeline, merge JSONL files from all machines in one site and
sort by `timestamp`. Keep clocks synchronized because cross-machine ordering in
local operational files depends on UTC wall clocks. Domain-event order remains
the journal sequence, not the local timestamp.
