# Architecture

OPDL builds one platform binary for each machine in a project blueprint. The
builder resolves the machine's deployment descriptor, embeds it in the runtime,
and packages the result. A package runs one process when warm standby is
disabled, or a primary and standby when it is enabled. Identity is
compiled into the artifact. A deployment site does not assign identity through
runtime configuration.

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

Every built platform binary embeds one `deployment.Descriptor`. It contains the
project, environment, site, machine, role, IP, services, features, resolved
warm-standby policy, and Event Fabric peers.

The runtime trusts the descriptor as its identity. Registration origins, event
nodes, and Event Fabric members come from it. A client cannot claim a different
machine or site. Runtime TOML configuration contains only site-adjustable
settings: the HTTP listen address, timeouts, the local instance directory, the
required projection lag bound, the journal's storage directory, a credentials
file, and Event Fabric socket overrides.

## Runtime boundaries

The platform is composed around three boundaries:

| Boundary | Responsibility |
| --- | --- |
| `internal/httpapi` | Decodes and encodes the public HTTP contract. It does not decide registration state. |
| `internal/registration` | Owns proposals, per-node decisions, acceptance, and conflict views. It depends only on the Event Fabric contract. |
| `internal/eventfabric` | Publishes facts to the site's ordered journal, replays them, delivers them, and reports health. Runtime code does not depend on a transport. |
| `internal/redundancy` | Owns process roles, lifecycle state, the machine fence, projection-lag state, and atomic status files. |
| `internal/operations` | Writes local structured operational events to stderr and optional JSONL without depending on the Event Fabric. |

The production adapter is `eventfabric/nats`. NATS is imported only by it.

## Event Fabric contract

The Event Fabric is the only coordination mechanism between OPDL services. It
exposes publish, replay, live delivery, durable handling, health, and shutdown,
and it hides every transport concept: no domain package constructs a subject,
stream, or consumer, or knows one exists.

| OPDL term | Responsibility |
| --- | --- |
| Journal | The site's ordered, retained event history. |
| Route | A stable destination derived from deployment scope and event type. |
| Receipt | Proof the journal durably accepted and ordered an event. |
| Projector | Rebuilds one node-local query model by folding the journal in order. |
| Handler | Reacts to selected routes for one service on one node, and may publish resulting facts. |
| Delivery | One journal event and the sequence the journal gave it. |

A route is `opdl.<site-scope>.event.<domain>.<fact>`, where `site-scope` is a
stable transport-safe hash of the length-prefixed project, environment, and site.
The journal is `OPDL_<UPPER_SITE_SCOPE>_EVENTS` and binds
`opdl.<site-scope>.event.>`. It uses file storage and rejects new events when a
configured byte limit is reached rather than deleting history a node still needs
to replay.

The fabric promises:

- publish returns only once the journal has durably accepted and ordered the
  event, so a receipt means the fact is retained and replayable;
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

Topology is derived, not discovered. Each machine authors two NATS ports in its
blueprint, and everything else is a consequence of the site:

```hcl
platform {
  nats {
    client_port  = 4222
    cluster_port = 6222
  }

  standby {
    disabled = false
  }
}
```

The builder joins each port with `machine.ip` and resolves the site's server and
route lists into the deployment descriptor. Those lists are never authored. A
blueprint that could state them directly could split a site, omit a storage node,
or point a machine at another site's journal, and the resulting descriptor would
look like a working one.

Runtime configuration carries no socket topology at all, and a configuration file
that sets one is rejected at load time rather than ignored. Addresses are
deployment data; a site owns where its journal is stored, not where the site's
journal is.

#### One endpoint set per machine, shared by both processes

A machine has one client port and at most one cluster port however many processes
it runs. The primary and standby are mutually exclusive owners of them: the
machine fence is released only after the active process has closed its embedded
server, so the process that next acquires the fence binds the same addresses.

This is why promotion does not change the address other machines were told to
connect to, why a client-only standby reaches the journal on the address the
active process is serving, and why the firewall inventory is one client port and
at most one cluster port per storage machine.

#### What actually listens

The cluster listener is bound only when the site's topology resolves routes.
Authoring a cluster port is not permission to bind it: with one storage node
there is no peer server, and binding it would open a port nothing can connect to.

| Site size | Storage machines | Cluster listeners | NATS monitor listeners |
| ---: | ---: | ---: | ---: |
| 1 | 1 | 0 | 0 |
| 2 | 1 | 0 | 0 |
| 4 | 3 | 3 | 0 |

There is no NATS monitoring listener. `HTTPPort` and `HTTPSPort` are left at
zero, which is what makes the embedded server start none. The runtime reads
connection state, journal high-water, projection progress, and lag through the
Event Fabric client API and writes them to per-process status files. It also
writes structured local operational events to stderr and optional JSONL. Those
events deliberately do not depend on NATS, so they remain available to explain a
connection or journal outage. A second unauthenticated HTTP surface would add an
open port without adding a signal. Any remote operational API is a separate
contract that must be platform-owned, authenticated, and authorized, and must
not proxy the NATS monitor.

Port reduction is not by itself a security control. Bind only to the machine's
exact `ip`, never a wildcard; restrict the client port to the OPDL machines that
need Event Fabric access and the cluster port to the selected storage machines;
keep credentials out of the blueprint and descriptor. The current
username/password configuration does not encrypt client or route traffic, so TLS
or mutual TLS is required before non-loopback NATS is suitable for an untrusted
network.

Storage nodes are selected deterministically by sorted machine name: one for a
site smaller than three machines, the first three otherwise, with one and three
journal replicas respectively. A two-member metadata group would lose quorum when
either member failed, which is worse than one member that either works or does
not.

One- and two-machine sites are POC deployments with no journal-node failure
tolerance. A deployment that must tolerate one journal node failure requires at
least three machines with stable storage on the first three machines by sorted
name. This protects event history, not service processes.

`scenarios.TestFourMachineStorageTopologyAndFailure` is the evidence for the
three-storage-node topology. It builds four machines from one blueprint, and
proves that exactly the first three by sorted name store the journal and bind a
cluster listener, that the fourth is client-only and binds nothing, that
publication through one machine is replayed through another, that the site keeps
accepting and projecting after one storage machine is stopped, and that the
stopped machine rejoins its own storage and reconverges to the same state.

A client-only machine's resolved server order is stable. The four-machine
scenario proves it reconnects and resumes projection after the storage server it
is connected to is killed. The remaining product decision is tracked in
`docs/backlog/event-fabric.md`: whether the platform should absorb the brief
window after a storage machine is lost during which the journal's replica group
is electing a leader and rejects writes.

The site's NATS cluster is exactly its storage nodes. Every other machine of the
site runs no server and reaches the journal as a client of the storage nodes.
This is not a simplification for its own sake: NATS sizes a journal's metadata
group from the routes a server is configured with, not from the servers that
actually hold storage, so a server in the cluster that stores nothing would
enlarge the quorum deciding whether the site can write without adding anywhere to
write to. A machine that does not store the journal therefore depends on one that
does, and does not start until it can reach it.

## Domain events

Events are facts stated by the package that owns the state transition. They are
not log messages. Publishing is synchronous: a fact is in the site's journal
before the operation that caused it returns.

| Package | Events |
| --- | --- |
| `internal/registration` | `platform.registration.proposed`, `confirmed`, `rejected`, `accepted` |
| `internal/app` | `platform.event_fabric.ready`, `stopping` |

`internal/events` owns only the envelope and its stamping. Every record is
self-describing: it carries its own occurrence ID, event type, payload schema
version, occurrence time, source subsystem, and the deployment node that stated
it, plus optional causal links. It deliberately carries no transport ordering; the
journal orders events when it accepts them, and that sequence belongs to the
Event Fabric receipt and delivery, not to the immutable fact.

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
2. Start the embedded NATS server, on a storage node, and connect.
3. Create or validate the site journal.
4. Attach the node-wide ordered projector and catch up to a captured high-water
   sequence.
5. Attach the durable handlers and let them work through what the journal
   retained for them.
6. Catch up again to whatever that work published.
7. State `platform.event_fabric.ready` and wait for the node's own projection to
   apply it.
8. Serve the public HTTP API.

A warm standby opens only a client Event Fabric connection and a distinct
projector. It catches up and follows the journal, writes its local process status,
and owns no public listener, durable handler, lifecycle readiness publication,
embedded NATS server, or JetStream storage.

It composes exactly the same adapter configuration as the active process from the
same descriptor, and then drops what it must not own: server ownership, the local
listener addresses, the routes, and the data directory. It keeps the resolved
server list whole. That list is what it reaches the journal through, and on a
storage machine its first entry is the address the active process is serving on
right now. Deriving a separate standby endpoint is what previously left the
standby retrying against an address nothing was listening on, so no process role
selects a different server list or listener address.

At startup each process prints the endpoints it actually composed, which is what
distinguishes an active storage server from a client-only local standby, a
client-only non-storage machine, and a storage node that has just been promoted:

```text
platform: event fabric configuration endpoint=10.0.1.10:4222 binds=true
  cluster=none servers=10.0.1.10:4222 routes=none storage=true replicas=1
```

`endpoint` is the machine's own address from the descriptor and is printed
whether or not this process binds it, so a standby and the active process it
follows are visibly talking about the same endpoint.

The primary and standby contend for one non-expiring OS file lock under the
configured local instance directory. Only the lock holder may compose active
capabilities. The lock is released after active resources close, or
automatically when the holding process exits. A standby waits for the fence
independently of its projector. After acquisition it marks itself activating,
closes the client-only composition, opens the active Event Fabric, catches up
again, drains retained handler work, publishes readiness, binds HTTP, and marks
itself active.

The whole readiness sequence is bounded by `catch_up_timeout`. A node that cannot
finish it does not serve, and reports how far its projector got and what each
handler still owed. Answering registration queries from a projection that has not
seen the site's history would be answering for a site the process has not caught
up with.

Shutdown reverses ownership. HTTP intake stops and in-flight requests drain;
handlers stop and finish the delivery they hold, because they are the only role
that causes new facts and a node that is leaving should not still be deciding for
the site; the node states `platform.event_fabric.stopping` while the journal can
still accept it; then the projector stops and the transport closes. A projector or
handler that stops on its own also ends serving: the projection is what every
query is answered from.

Full-machine shutdown stops the primary service and then the standby service. A
standby may promote during this bounded interval and is stopped immediately.

The site journal is the platform's durable state. A node rebuilds its projections
by replaying it at every start, so a machine that is killed comes back to the same
answers. Local projections are memory-only and are not snapshotted; replay cost
has not yet justified it.

## Local warm standby

OPDL can run a preferred primary and an optional standby for one machine. The
fence owner runs active capabilities; the other process maintains a warm local
projection. Both retain the same compiled machine identity, so they remain one
registration voter. After failover, a returning primary reclaims ownership only
through graceful handover from the promoted standby.

After failover, a returning primary starts projection-only and waits. Deployment
tooling verifies that it is caught up, gracefully stops the promoted standby,
and waits for the primary to acquire the released fence. The primary never
steals ownership from a live standby. Journal replication remains separate from
service redundancy: storage replicas protect site history, while the local
process fence protects one machine's active capabilities.

### Package and service-manager contract

Each package manifest contains one required `primary` launch and, when the
machine's blueprint sets `standby.disabled = false`, one optional `standby`
launch. Both name the same binary and configuration. Their direct arguments are
`-instance primary` and `-instance standby`.

The standby decision is never inferred from an omitted field. Every blueprint
machine states `platform.standby.disabled`, every descriptor carries explicit
`slots.primary.disabled` and `slots.standby.disabled` records, and the platform
refuses to decode a descriptor that omits either. A missing decision is a startup
error rather than a default.

Deployment starts the primary and waits for its local status to become `active`
before starting the standby. A handover requires a fresh, live standby status
with `promotable=true`, no last error, and a caught-up sequence. The service
manager then gracefully stops the active process and waits for the other process
to become `active`. A returning primary uses this procedure to reclaim
ownership. Full machine shutdown stops the primary service and then the standby
service; the standby may briefly promote between those operations.

Status files are operational evidence, not ownership. They identify the process
role and PID and report lifecycle state, projection progress, lag, promotability,
and the last error. Only the OS fence grants active ownership.

### Validation baseline

The black-box warm-standby scenario builds a standby-enabled package, launches
both processes, kills and hands ownership over repeatedly, preserves
registrations, checks storage ownership, reclaims the preferred primary, and
completes full machine shutdown. It records measurements without enforcing an SLO.

It also asserts the endpoint contract directly: that the standby binds nothing
while the primary holds the fence, that it nonetheless composes the same machine
endpoint and the same server list as the active process, and that the promoted
process rebinds those same addresses rather than moving the site onto new ones.

Three consecutive Windows development runs after the shared-endpoint change:

| Measurement | Run 1 | Run 2 | Run 3 |
| --- | ---: | ---: | ---: |
| Initial standby journal catch-up | 110.8 ms | 108.3 ms | 91.9 ms |
| Forced-kill promotion | 182.6 ms | 126.8 ms | 149.4 ms |
| Listener unavailable | 192.9 ms | 133.5 ms | 155.2 ms |
| Planned handover | 275.8 ms | 266.6 ms | 177.8 ms |

The earlier 2026-07-18 baseline measured a forced-kill promotion of about 29.9
seconds. That gap was a symptom rather than a performance property: the standby
had been given its own NATS client address while the only running server was the
active process's, so it never reached the journal and was never warm, and the
promoted process had to complete a cold startup bounded by the same 30 second
Event Fabric startup timeout.

These remain development measurements, not production limits or percentiles.
Three samples on one host are not an SLO, and none may be quoted as one until
`docs/backlog/redundancy.md` records cross-platform CI percentiles.

