# Events

OPDL has one event contract for instance, machine, and site facts. The contract
and lower-level storage are implemented. Site distribution is not.

## Current state

| Part | State |
| --- | --- |
| Event contract, envelope, factory, and scope | Implemented in `platform/internal/events` |
| Synchronous publisher and backend contract | Implemented in `platform/internal/events/storage` |
| Instance event log | Implemented as append-only JSONL |
| Machine event store | Implemented as append-only JSONL |
| Site delivery contract | Defined in `platform/internal/site/eventfabric` |
| Site distribution and durable consumer positions | Not implemented |
| Registration event publication and handling | Not implemented |

The runtime opens the instance and machine files before ownership management.
`app.hasEventStorage` is still hardcoded `false`, so the site path is
unreachable.

## Event model

Events are immutable facts stated by the package that owns a transition. They
are not diagnostic log messages. Concrete payloads live in the owning package's
`events.go`.

An event type has the form `platform.<source>.<fact>`. The source is derived from
the type. Payloads implement `events.Event`; optional interfaces override
defaults for schema version, severity, scope, tags, or stable identity.

One `events.Factory` per process wraps payloads in an `events.Envelope`. The
envelope contains:

- occurrence ID and UTC time;
- type, derived source, schema version, and severity;
- scope;
- immutable deployment and process origin;
- optional causation, correlation, tags, and stable identity; and
- the JSON payload.

The factory stamps origin and occurrence metadata. Domain code cannot claim a
different machine or process. Transport order is not part of the envelope.

## Scope and destinations

Scope states which level owns a fact. It adds destinations but never removes the
instance log.

| Scope | Instance event log | Machine event store | Site distribution |
| --- | --- | --- | --- |
| `instance` | yes | no | no |
| `machine` | yes | yes | no |
| `site` | yes | no | planned |

`instance` is the default. A missing scope declaration therefore keeps a fact
local instead of leaking it to a wider level. Catalog tests verify events that
must use a wider scope.

A package level does not determine its events' scopes.
`internal/machine/redundancy`, for example, states both instance and machine
facts. Routing is decided from each stamped envelope.

## Publication and storage

`events/storage.Publisher` stamps an event once and calls each configured backend
synchronously in order. It calls later backends even after an earlier failure
and returns all failures with `errors.Join`.

This is fan-out, not a transaction. One backend may accept an envelope before
another fails. Callers must propagate errors when the wider fact is required.
Background paths that cannot return an error use `events.BestEffort` and report
the failure through a diagnostic sink.

The current process publisher has these backends:

1. `instance/eventlog.Backend`, which stores every valid envelope.
2. `machine/eventstore.Backend`, which ignores non-machine scopes and appends
   machine-scoped envelopes.

The machine appender itself rejects a non-machine envelope. The difference is
intentional: a routing backend sees every envelope, while a direct appender call
is an explicit claim that the fact belongs to the machine.

Both lower-level stores are append-only and fsync each accepted JSONL line.
Neither exposes a read API because no platform behavior consumes either file.
Operators, scenarios, and external tooling may read them.

## Current catalogs

| Package | Scope |
| --- | --- |
| `internal/app` | instance |
| `internal/machine/redundancy` lease-open, waiting, declined-promotion, and renewal-attempt facts | instance |
| `internal/machine/redundancy` ownership, failback, and activation transitions | machine |
| `internal/site/registration` | site |

Every fact still appears in the event log of the process that stated it.
Machine-scoped facts also give the machine one continuous ownership account
across primary and standby processes.

## Site Event Fabric contract

`internal/site/eventfabric` currently defines only the read-side contract needed
by registration:

- `Delivery` pairs an envelope with the site's sequence;
- `Consumer.Follow` replays unacknowledged deliveries and then follows live;
- `Consumer.Ack` records a durable, machine-scoped consumer position; and
- delivery is at least once.

Site publication uses the common `events.Publisher`; the package does not define
a second publisher type. A concrete site adapter still needs to integrate with
the scoped backend flow, accept only site-scoped envelopes, assign site order,
and implement durable consumption.

The remaining design decision is how multiple machines share one durable order.
The implementation must preserve registration's conflict rule or deliberately
replace that rule with a convergent one. See the
[hierarchy plan](plans/hierarchy/README.md).

## Registration dependency

Registration already declares site-scoped payloads and folds
`eventfabric.Delivery` values into a deterministic projection. Its command,
handler, and runtime composition are still stubs. Until site distribution lands,
the public registration API remains unavailable. See
[Registration](03-registration.md).
