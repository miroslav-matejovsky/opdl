# Architecture

OPDL builds one platform binary for each machine in a project blueprint. The
builder resolves the machine's deployment descriptor, embeds it in the runtime,
and packages the result. A package runs one process when warm standby is
disabled, or a primary and standby when it is enabled. Identity is compiled into
the artifact; a deployment site does not assign identity through runtime
configuration.

## Distribution line

```text
project blueprint
      |
      v
builder -> resolved descriptor per machine -> platform binary per machine
                                                |
                                                v
                                      primary and optional standby
```

The repository is a Go workspace of four modules plus a generated .NET SDK.

| Module | Responsibility |
| --- | --- |
| `builder` | Reads `examples/*/project.hcl`, derives deployment descriptors, and packages one platform binary per machine. |
| `platform` | Runs on a machine, serves the registration HTTP API, and coordinates through its site's event journal. |
| `conformance-tests` | Regenerates the OpenAPI contract and .NET SDK, and checks builder and platform descriptor compatibility. |
| `scenarios` | Drives built binaries as external processes for black-box tests. |
| `sdk-dotnet` | Contains the Kiota-generated .NET client and its end-to-end tests. |

The public API types in `platform/api` are the source for
`api-specifications/openapi.yaml`. The conformance tests generate the OpenAPI
artifacts and then regenerate `sdk-dotnet` from that contract.

## Deployment descriptor

Every built binary embeds one `deployment.Descriptor`, and it is the only
configuration that binary has. There is no runtime configuration file: a launch
decides one thing, `-instance primary|standby`. The runtime trusts the descriptor
as its identity, so a client cannot claim a different machine or site.

The descriptor states the machine once and each instance separately:

| Part | Contents |
| --- | --- |
| machine | platform, project, environment, site, machine, machine profile, ip, services |
| `primary` | mandatory: Windows Service identity, data dir, loopback API address, listener timeouts |
| `standby` | the same fields, present only when the machine deploys a standby |
| `lease` | the shared Primary Ownership file, its failover timings, and the projection lag bound; present exactly when `standby` is |

A machine with no standby carries neither a standby record nor a lease, so an
endpoint in a descriptor is always one a process will bind, and the two absences
say the same thing once rather than a disabled record saying it in three places.
See `builder/deployment` for the field-level contract and `platform/config` for
the runtime's copy of it; the two are kept in step by `conformance-tests`.

The listener timeouts are per instance, authored in each `api` block, because the
two instances bind their own listeners. The lag bound is on `standby.lease`,
because it bounds a failover: a machine that deploys no standby has no lease and
no lag bound.

A site changes any of this by rebuilding the machine's package, which is what it
already did for every endpoint and every path. What it gains is that a binary
states its own configuration: nothing can be turned beside the executable, and no
setting has two sources needing a precedence rule to tell them apart.

## Runtime boundaries

| Boundary | Responsibility |
| --- | --- |
| `internal/httpapi` | Decodes and encodes the public HTTP contract. It does not decide registration state. |
| `internal/registration` | Owns proposals, per-node decisions, acceptance, and conflict views. It depends only on the Event Fabric contract. |
| `internal/instance/events` | Owns the event contract, envelope, factory, and producer-facing publisher interface. |
| `internal/instance/events/storage` | Fans one stamped envelope out synchronously to configured storage backends. |
| `internal/instance/events/storage/jsonl` | Writes the mandatory process-local JSONL record under the instance data root. |
| `internal/machine/redundancy` | Owns process roles, the active/passive state, Primary Ownership, and projection-lag state. It writes no files. |

## Event Fabric contract

> **TODO — this section is a design, not the current tree.** Event storage and
> the Event Fabric were removed during the ongoing refactor, and the replacement
> distribution mechanism has not landed. Until it does:
>
> - no deployment has event storage. `app.hasEventStorage` is hardcoded false, so
>   every instance takes the journal-less path, opens no site, and runs no
>   projection or readiness monitor.
> - **registration is blocked on this.** `internal/registration` is built around
>   a publisher, a projector, and a durable handler over the site journal, so with
>   no journal every domain operation is refused with a reason naming the
>   deployment rather than the instance. See `docs/02-registration.md`.
> - the projection lag bound authored on `standby.lease` is carried and validated
>   but never consulted, because there is no journal to lag behind.
> - the mandatory local JSONL record is unaffected. It is the only event surface
>   that currently works, and process and ownership events still reach it.

The Event Fabric is the only coordination mechanism between OPDL services. It
exposes publish, replay, live delivery, durable handling, health, and shutdown,
and it hides every transport concept: no domain package constructs a subject,
stream, or consumer, or knows one exists.

| OPDL term | Responsibility |
| --- | --- |
| Journal | The site's ordered, retained event history. |
| Route | A stable destination derived from deployment scope and event type. |
| Publisher | Stamps a typed payload once and stores the envelope through its configured backends. Business code is handed this and nothing else. |
| Backend | Stores one already stamped envelope without deciding what the fact says. |
| Projector | Rebuilds one node-local query model by folding the journal in order. |
| Handler | Reacts to selected routes for one service on one node, and may publish resulting facts. |
| Delivery | One journal envelope and the sequence the journal gave it. |

A route is `opdl.<site-scope>.event.<domain>.<fact>`, where `site-scope` is a
stable transport-safe hash of the length-prefixed project, environment, and site.
There is one journal per site, holding `opdl.<site-scope>.event.>`. It rejects
new events when a configured byte limit is reached rather than deleting history a
node still needs to replay.

The fabric promises:

- a successful site publication means both mandatory JSONL and the journal
  accepted the same envelope;
- one ordered journal per site, whose order is the common order every projection
  folds;
- at-least-once delivery, so every projector and handler is idempotent by event
  identity and transport deduplication is an optimization, never correctness;
- replay from the first retained event through live delivery on one consumer, so
  there is no gap between the two;
- durable per-service, per-node handlers with explicit acknowledgement, so a node
  that was offline still receives what it missed;
- context cancellation and consistent behavior after close.

A malformed or unsupported event stops a projector rather than being skipped: a
node that cannot fold its site's history must become unready, not answer from a
view with a hole in it.

### Site topology

Topology is derived, not discovered. Each deployed instance authors every local
file it owns, its API port, and the timeouts bounding that listener. Everything
else is a consequence of the site:

```hcl
platform {
  events_file = "D:/opdl/customer-a/north/local-server/primary/events.jsonl"
  state_file  = "D:/opdl/customer-a/north/local-server/primary/state.json"

  api {
    local_port          = 8080
    read_header_timeout = "5s"
    shutdown_timeout    = "10s"
  }

  winservice {
    name         = "opdl-customer-a-north-local-server-primary"
    display_name = "OPDL customer-a north local-server (Primary Instance)"
  }

  standby {
    disabled    = false
    events_file = "D:/opdl/customer-a/north/local-server/standby/events.jsonl"
    state_file  = "D:/opdl/customer-a/north/local-server/standby/state.json"

    lease {
      file                   = "D:/opdl/customer-a/north/local-server/lease"
      duration               = "15s"
      renewal_interval       = "5s"
      health_check_interval  = "2s"
      failback_stabilization = "30s"
      lag_bound              = "30s"
    }

    api {
      local_port          = 8081
      read_header_timeout = "5s"
      shutdown_timeout    = "10s"
    }

    winservice {
      name         = "opdl-customer-a-north-local-server-standby"
      display_name = "OPDL customer-a north local-server (Standby Instance)"
    }
  }
}
```

The `winservice` blocks name the Windows Service that runs each of the machine's
two fixed instances. See the fixed-role model below.

Site-wide lists are resolved by the builder, never authored. A blueprint that
could state them directly could split a site, omit a storage node, or point a
machine at another site's journal, and the resulting descriptor would look like a
working one.

Primary and Standby Instances have distinct files and ports because they are
separate processes on one host. Every path is authored outright rather than
composed under a shared root, and the builder rejects a machine that resolves two
of them onto one file. Primary Ownership controls domain handlers and serving,
not journal membership.

### Instance epoch

Each instance carries a `state_file` across restarts and crashes. It holds an
epoch: a counter that advances by exactly one when the process starts and again
when the instance takes Primary Ownership. Stepping down does not advance it.

The epoch is the only thing about an instance that distinguishes one incarnation
from the next; its machine, role, and address are identical after a crash. It is
durable so the sequence is monotonic for the whole life of a deployed instance,
which is what a fencing token needs: a reader holding an instance's epoch can
reject anything stamped with an older one. An instance that cannot record a new
epoch stops rather than running under a number a restart would hand out again.
See `docs/drafts/data-priority.md` for where this is going.

The record keeps the two kinds apart as well as their total: how many times the
instance has been launched, how many times it has activated, and when each of
those last happened, in UTC. Only the total fences writes, because neither count
alone is monotonic in the order the two kinds interleaved. The counts are what
say _why_ the total moved: an instance on epoch six that started once and
activated five times is a machine whose ownership keeps moving, and one that
started five times and activated once is a machine whose process keeps dying.
Both facts reach the local event record too, as
`platform.app.epoch_advanced`, so reading them does not mean holding the state
file open.

The platform API is machine-local: every API address is resolved onto `127.0.0.1`
and none is reachable from the network. Any remote operational API would be a
separate contract that must be platform-owned, authenticated, and authorized.

Storage nodes are selected deterministically by sorted machine name: one for a
site smaller than three machines, the first three otherwise, with one and three
journal replicas respectively. A two-member metadata group would lose quorum when
either member failed, which is worse than one member that either works or does
not.

One- and two-machine sites are POC deployments with no journal-node failure
tolerance. A deployment that must tolerate one journal node failure requires at
least three machines with stable storage on the first three machines by sorted
name. This protects event history, not service processes.

There is currently no scenario evidence for the three-storage-node topology. The
four-machine storage scenario that proved it was removed with the rest of the
suite; the scenarios now cover only the single-machine floor. The remaining
product decision is tracked in `docs/backlog/event-fabric.md`: whether the
platform should absorb the brief window after a storage machine is lost during
which the journal's replica group is electing a leader and rejects writes.

## Events

Events are facts stated by the package that owns the state transition. They are
not log messages. Publication is synchronous. Site facts are retained in local
JSONL and the journal before the operation that caused them returns.

The platform has one event model and one serialized wrapper. These invariants
hold everywhere:

- concrete event payloads live in the owning package's `events.go`, never in a
  central catalog and never at a call site;
- common metadata is stamped automatically by one factory per process, so
  business code constructs a typed payload and nothing else;
- an event type is `platform.<source>.<fact>`, and the source is derived from it
  rather than restated;
- transport order is delivery metadata, not part of the fact;
- process-local event output stays usable while the journal is unavailable,
  because it uses the JSONL-only publisher;
- storage and distribution do not define event shape; an adapter receives a
  completed envelope and decides only where it goes.

| Package | Events | Written to |
| --- | --- | --- |
| `internal/registration` | `platform.registration.proposed`, `confirmed`, `rejected`, `accepted` | JSONL and site journal |
| `internal/app` | `platform.app.<fact>`: process, status, API, standby, projection, and site transitions | JSONL |
| `internal/machine/redundancy` | `platform.redundancy.<fact>`: ownership and activation transitions | JSONL |

`internal/instance/events` owns the contract and the envelope and declares no events of
its own. An event payload implements one method, `EventType`, and implements a
small optional interface only where it differs from a default: a schema version
other than `1`, a severity other than `info`, tags, or a domain-stable identity.

`events.Envelope` is the only serialized wrapper, and every envelope is validated
before it leaves the process, so a stored event is always self-describing. It
deliberately carries no transport ordering: the journal orders events when it
accepts them, and that sequence belongs to delivery metadata, not to the
immutable fact.

Causal links are infrastructure, not domain vocabulary. The Event Fabric attaches
a delivery to the handler's context as the cause before invoking it, so a
consequence records what produced it and which workflow both belong to without
the handler knowing that causation exists.

Origin names the deployment down to the machine, plus the writing process's role
and PID. The deployment half is domain identity; the process half is operational
identity for an operator only. Domain behavior stays machine-scoped, so a machine
running two instances is still one registration voter.

The Event Fabric's own lifecycle events are stated by runtime composition, not by
the adapter. Readiness is a conclusion about a whole node — its journal, its
projections, and its handlers — and a transport can only report on itself. There
is deliberately no `stopped` event: a closed transport cannot durably record its
own close, so the absence of a later `ready` is the only honest evidence a node
stopped.

## Bootstrap and lifecycle

Active startup order is strict, and each step exists because the next one would
otherwise be a lie:

1. Validate the configuration and probe the journal's storage.
2. Connect to the site journal, creating or validating it.
3. Attach the node-wide ordered projector and catch up to a captured high-water
   sequence.
4. Attach the durable handlers and let them work through what the journal
   retained for them.
5. Catch up again to whatever that work published.
6. State `platform.event_fabric.ready` and wait for the node's own projection to
   apply it.
7. Serve the public HTTP API.

The whole readiness sequence is bounded. A node that cannot finish it does not
serve, and reports how far its projector got and what each handler still owed.
Answering registration queries from a projection that has not seen the site's
history would be answering for a site the process has not caught up with.

A warm standby opens its own journal connection and projector. It catches up and
follows the journal, writes its local process record, and owns no durable
handler, readiness publication, or domain operation.

Shutdown reverses ownership. HTTP intake stops and in-flight requests drain;
handlers stop and finish the delivery they hold, because they are the only role
that causes new facts and a node that is leaving should not still be deciding for
the site; the node states `platform.event_fabric.stopping` while the journal can
still accept it; then the projector stops and the transport closes. A projector or
handler that stops on its own also ends serving: the projection is what every
query is answered from.

Full-machine shutdown stops the primary service and then the standby service. A
standby may become Active during this bounded interval and is stopped
immediately.

The site journal is the platform's durable state. A node rebuilds its projections
by replaying it at every start, so a machine that is killed comes back to the
same answers. Local projections are memory-only and are not snapshotted; replay
cost has not yet justified it.

## Local warm standby

### Fixed instance roles

A machine runs two instances with fixed roles: a **Primary Instance**, always
deployed, and a **Standby Instance**, deployed when the machine's blueprint
enables one. The role is decided at build time from the blueprint, carried in the
package, and named in the Windows Service that runs the process. It is not
assigned at runtime, negotiated, or exchanged.

A role is not a state. A Standby Instance that takes over does not become the
Primary Instance: it runs active capabilities until ownership returns. The two
axes, `role` and `state`, are reported separately, and the combinations that
differ are the interesting ones, `standby`/`active` most of all.

Each instance's Windows Service name is authored in the blueprint's `winservice`
blocks and carried into `manifest.json`, so the same two roles are named
identically on every machine. The builder rejects a machine whose two instances
name the same service, since they share a host. The platform installs and manages
no services and has no Service Control Manager integration; the names are a
declaration for whoever installs them, and each instance prints its own at
startup.

### Ownership

Primary Ownership is a finite, renewable lease recorded in a machine-wide file
the descriptor names. The owner renews it while Active; a Standby takes it over
once it lapses and the peer's health endpoint reports the owner can no longer
serve. It is released cleanly after active resources close, or left to lapse when
the process dies, so a crash-caused failover is distinguishable from a planned
handover in `platform.redundancy.ownership_acquired`.

Under the Preferred Primary policy an Active Standby hands ownership back once
the returned Primary has been continuously healthy for the stabilization window.
The Primary never seizes ownership from a live Standby. Both instances carry the
same compiled machine identity, so they remain one registration voter, and
journal replication stays separate from service redundancy: replicas protect site
history, the lease protects one machine's active capabilities.

The lease replaced a non-expiring Windows named mutex, which gave mutual
exclusion by construction but could never fail over from an unresponsive-but-
alive holder. Split-brain is kept out instead by three combined means: the two
instances share one host's clock, so an expiry means the same instant to both; an
owner that cannot renew steps down before its lease could lapse from a promoter's
view; and a promoter takes over only when the peer is also unhealthy.

Both instances bind their own loopback API address for their whole lifetime, so a
transfer swaps the handler behind an already-open listener rather than moving an
address between processes. An instance's data directory takes no part in
ownership at all: the directories are operational evidence, so two of them are
two places to read an instance's own record rather than two ownership scopes.

### Package and service-manager contract

Each package manifest contains one required `primary` launch and, when the
machine's blueprint sets `standby.disabled = false`, one optional `standby`
launch. Both name the same binary. Their direct arguments are `-instance primary`
and `-instance standby`.

The standby decision is never inferred from an omitted field. Every blueprint
machine states `platform.standby.disabled`, and the resolved descriptor either
carries a complete standby record with its lease or carries neither.

Deployment starts the primary and waits for it to state `platform.app.api_active`
before starting the standby. A handover requires the standby's last
`platform.app.failover_readiness_changed` to say `ready`, from the PID of the
process that is running now. The service manager then gracefully stops the active
process and waits for the other process to state `platform.app.api_active`. Full
machine shutdown stops the primary service and then the standby service; the
standby may briefly become Active between those operations.

The local record is operational evidence, not ownership. Every event on it
identifies the process role and PID that stated it, and between them the events
report lifecycle transitions, projection progress, lag, and failover readiness.
Only Primary Ownership makes an instance Active.

There is no status file. Readiness is stated when it changes, and liveness is
asked of the instance's own API, which is bound in every state, rather than read
from a file a dead process leaves behind unchanged.

### Validation baseline

The black-box warm-standby scenario builds a standby-enabled package, launches
both processes, kills and hands ownership over repeatedly, preserves
registrations, fails back to the Primary Instance, and completes full machine
shutdown. It records measurements without enforcing an SLO.

Three consecutive Windows development runs with the file lock, followed by one
run after it was replaced with the named mutex:

| Measurement | Lock 1 | Lock 2 | Lock 3 | Mutex |
| --- | ---: | ---: | ---: | ---: |
| Initial standby journal catch-up | 110.8 ms | 108.3 ms | 91.9 ms | 144.1 ms |
| Forced-kill failover | 182.6 ms | 126.8 ms | 149.4 ms | 86.6 ms |
| Listener unavailable | 192.9 ms | 133.5 ms | 155.2 ms | 91.8 ms |
| Operator-initiated failback | 275.8 ms | 266.6 ms | 177.8 ms | 164.0 ms |

The three ownership-transfer rows improved by roughly the amount the removed poll
predicts. The file lock woke a waiter on its next 100 ms tick; the mutex woke it
in the kernel when the holder released or died. Catch-up is not an ownership
measurement and its one mutex sample is within the noise of a single run.

These remain development measurements, not production limits or percentiles. One
sample on one host is not an SLO, and none may be quoted as one until
`docs/backlog/redundancy.md` records percentiles from more than one host.
