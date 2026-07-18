# Architecture

OPDL builds one platform binary for each machine in a project blueprint. The
builder resolves the machine's deployment descriptor, embeds it in the runtime,
and packages the result. A package runs one process when warm standby is
disabled, or two independently launched slots when it is enabled. Identity is
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
                                      one or two local slots
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
| `internal/redundancy` | Owns local slot identity, lifecycle state, the machine fence, projection-lag state, and atomic status files. |

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

Topology is derived, not discovered. The builder resolves each machine's site
peers, and the adapter derives everything else from them on fixed ports: `4222`
for clients, `6222` for cluster routes, `8222` for monitoring on loopback.
Runtime overrides move sockets and storage for development and tests but never
change machine identity.

Storage nodes are selected deterministically by sorted machine name: one for a
site smaller than three machines, the first three otherwise, with one and three
journal replicas respectively. A two-member metadata group would lose quorum when
either member failed, which is worse than one member that either works or does
not.

One- and two-machine sites are POC deployments with no journal-node failure
tolerance. A deployment that must tolerate one journal node failure requires at
least three machines with stable storage on the first three machines by sorted
name. This protects event history, not service processes.

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
projector. It catches up and follows the journal, writes its local slot status,
and owns no public listener, durable handler, lifecycle readiness publication,
embedded NATS server, or JetStream storage. On a storage machine it prefers the
co-located active server and retains peer storage addresses as fallbacks.

Both slots contend for one non-expiring OS file lock under the configured local
instance directory. Only the lock holder may compose active capabilities. The
lock is released after active resources close, or automatically when the
holding process exits. Stage 3 decides the role once at startup. Automatic
promotion after later fence acquisition is Stage 4 work.

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

Full-machine shutdown cannot use a static slot-name order because either slot
may be active. Deployment tooling reads live status, stops the current standby,
waits for it to exit, and then stops the current active.

The site journal is the platform's durable state. A node rebuilds its projections
by replaying it at every start, so a machine that is killed comes back to the same
answers. Local projections are memory-only and are not snapshotted; replay cost
has not yet justified it.

## Local warm standby

OPDL can run two symmetric process slots for one machine. One slot holds the
local fence and runs the active capabilities. The other maintains a warm local
projection. Both retain the same compiled machine identity, so they remain one
registration voter.

Stage 3 provides fencing and the projection-only standby, but it does not yet
promote a standby after the active exits. Journal replication also remains
separate from service redundancy: storage replicas protect site history, while
the local slot fence protects one machine's active capabilities. Promotion and
controlled handover are defined in [Stage 4](plan/04-promotion-and-handover.md).

