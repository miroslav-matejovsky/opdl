# Architecture

OPDL builds one platform binary for each machine in a project blueprint. The
builder resolves one deployment descriptor per machine, embeds it in the
runtime, and packages the binary. Identity and runtime settings are compiled
into the artifact.

## Distribution line

```text
project blueprint
      |
      v
builder -> deployment descriptor per machine -> platform binary per machine
                                                     |
                                                     v
                                           primary and optional standby
```

The repository is a Go workspace of four modules plus a generated .NET SDK.

| Module | Responsibility |
| --- | --- |
| `builder` | Loads project blueprints, resolves deployment descriptors, and packages one binary per machine. |
| `platform` | Runs the machine-specific binary and serves its loopback HTTP API. |
| `conformance-tests` | Regenerates API artifacts and checks builder/platform descriptor compatibility. |
| `scenarios` | Drives built binaries as Windows processes for black-box tests. |
| `sdk-dotnet` | Contains the generated .NET client and end-to-end tests. |

The public types in `platform/api` are the source for the generated OpenAPI
artifacts and .NET SDK.

## Deployment descriptor

A blueprint describes projects, sites, machines, and each machine's instances.
The builder validates it and resolves one `deployment.Descriptor` per machine.
The runtime has no configuration file beside the executable. A launch chooses
only `-instance primary|standby`.

| Descriptor part | Contents |
| --- | --- |
| Machine | platform, project, environment, site, machine, profile, IP, services, and `machine_events_file` |
| `primary` | mandatory service identity, local event, state, and log files, loopback API address, and listener timeouts |
| `standby` | the same instance fields, present only when deployed |
| `lease` | shared Primary Ownership file and failover timings, present exactly when `standby` is present |

The blueprint calls the machine file `eventstore_file` and the instance files
`eventlog_file`, `state_file`, and `log_file`. The resolved descriptor names them
`machine_events_file`, `events_file`, `state_file`, and `log_file`.

The machine event store exists on every machine. The lease exists only on a
machine with a standby. The builder rejects file collisions across instance
event logs, state files, application logs, the machine event store, and the
lease. It also rejects duplicate local API addresses and duplicate Windows
Service names on one machine.

`builder/deployment` and `platform/config` define independent copies of the
descriptor contract. `conformance-tests` keeps them compatible.

## Runtime hierarchy

Runtime packages are grouped by the owner of their state:

| Level | Identity | Current responsibilities |
| --- | --- | --- |
| Instance | one process in a fixed role | local event log, application log, durable epoch, and one loopback API |
| Machine | one Windows host | Primary Ownership, active/passive sequencing, and the shared machine event store |
| Site | all machines in one deployment site | site event contract and registration domain |

The detailed level documentation lives beside the code:

- [`internal/instance`](../platform/internal/instance/README.md)
- [`internal/machine`](../platform/internal/machine/README.md)
- [`internal/site`](../platform/internal/site/README.md)

### Runtime boundaries

| Boundary | Responsibility |
| --- | --- |
| `internal/app` | Composition root and process lifecycle. |
| `internal/httpapi` | Public HTTP decoding and encoding. It does not decide domain state. |
| `internal/events` | Level-independent event contract, envelope, factory, and publisher interface. |
| `internal/events/storage` | Synchronous fan-out to configured storage backends. |
| `internal/instance/eventlog` | Mandatory process-local JSONL event record. |
| `internal/instance/applog` | Structured application log written with `log/slog`. See [Logging](04-logging.md). |
| `internal/instance/state` | Durable per-instance epoch. |
| `internal/machine/redundancy` | Fixed roles, Primary Ownership, and active/passive sequencing. |
| `internal/machine/eventstore` | Shared append-only JSONL store for machine-scoped events. |
| `internal/site/eventfabric` | Site delivery and durable-consumer contract. No implementation yet. |
| `internal/site/registration` | Registration events, projection, commands, queries, and handler contract. |

### Dependency direction

A higher level may import a lower level, never the reverse. `internal/events`
is shared and imports no level.

| Package | May import |
| --- | --- |
| `internal/site/...` | `internal/machine/...`, `internal/instance/...`, `internal/events/...` |
| `internal/machine/...` | `internal/instance/...`, `internal/events/...` |
| `internal/instance/...` | `internal/events/...` |
| `internal/events/...` | none of the three levels |
| `internal/app`, `internal/httpapi` | any level as composition and edge packages |

`depguard` enforces the level rule during `task lint`.
`platform/.go-arch-lint.yml` applies the stricter package-level allow-list during
`task arch`.

## Process lifecycle

At startup a process:

1. Loads and validates the embedded descriptor.
2. Validates the requested instance role.
3. Opens the instance application log and installs it as the `slog` default.
4. Creates one event factory for the process.
5. Opens the instance event log and machine event store.
6. Opens and advances the instance state epoch.
7. Binds the instance's loopback HTTP listener.
8. Enters machine ownership management.

The application log is opened first so every later failure to open something has
somewhere to be described, and closed last.

The process event publisher currently has two ordered backends. The instance
event log receives every envelope. The machine backend receives only
machine-scoped envelopes. See [Events](02-events.md).

The listener stays bound for the process lifetime. Passive and Active states
swap the handler behind that listener, so ownership transfer never moves an
address between processes.

### Current site limitation

Site event distribution has not been implemented. `app.hasEventStorage` returns
`false`, so no site projection or durable handler is opened. Health endpoints
and `GET /instance` work. Registration operations use the journal-less handler
and return `503`.

The projection lag bound remains in the descriptor but is not consulted on this
path. The hierarchy plan tracks the remaining work in
[`docs/plans/hierarchy`](plans/hierarchy/README.md).

## Local redundancy

Every machine has a fixed Primary Instance. A Standby Instance is optional. Role
does not change at runtime:

| Role | Possible states |
| --- | --- |
| Primary Instance | Passive or Active |
| Standby Instance | Passive or Active |

Active state comes only from Primary Ownership. A Standby that takes ownership
remains the Standby Instance.

Primary Ownership is a finite lease in the machine-wide file named by the
descriptor. The owner renews it while Active. A Standby may acquire a lapsed
lease only after the peer also fails its loopback health check. If the owner
cannot renew, it steps down before another instance can treat the lease as
expired.

The preferred-primary policy hands ownership back after the Primary has remained
healthy for the configured stabilization interval. The Primary does not seize
ownership from a healthy Active Standby.

Ownership and site distribution are separate. A site transport failure must not
break local ownership transfer. Until site distribution exists, an owner serves
only the journal-less API surface.

## Instance epoch

Each instance has its own `state_file`. Its epoch advances before the process
runs and again before an activation serves. Stepping down does not advance it.
Process-start and activation counts are stored separately for diagnostics.

An advance is written atomically before the new value is returned. An unreadable
existing state file is a startup failure. The two instances have independent
epochs; neither reads the other's state file.

## Windows Service contract

Each package manifest has one required primary launch and, when configured, one
standby launch. Both use the same binary with `-instance primary` or
`-instance standby`.

Windows Service names are deployment declarations. The platform does not install
or control services. It currently handles `os.Interrupt`; full Service Control
Manager integration remains unfinished.

## Validation baseline

The current black-box coverage proves:

- a primary-only machine builds and runs without site event storage;
- registration is refused because no site journal exists;
- a standby-enabled machine fails over after a forced primary kill;
- ownership returns to the preferred Primary after recovery;
- the machine event store contains the machine-scoped ownership sequence across
  both processes;
- instance event logs contain process-local facts;
- each instance writes its structured application log to its authored
  `log_file`, with every record naming the instance that wrote it; and
- instance epochs survive restart and activation.

Registration persistence and multi-machine convergence are not part of the
current scenario baseline.
