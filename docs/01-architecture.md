# Architecture

OPDL builds one platform binary for each machine in a project blueprint. The
builder resolves the machine's deployment descriptor, embeds it in the runtime,
and packages the result. Identity is compiled into the artifact. A deployment
site does not assign identity through runtime configuration.

## Distribution line

```text
project blueprint
      |
      v
builder -> resolved descriptor per machine -> platform binary per machine
                                                |
                                                v
                                      one runtime at the site
```

The repository is a Go workspace of four modules plus a generated .NET SDK.

| Module | Responsibility |
| --- | --- |
| `builder` | Reads `examples/*/project.hcl`, derives deployment descriptors, and packages one platform binary per machine. |
| `platform` | Runs on a machine, serves the registration HTTP API, and records domain events. |
| `conformance-tests` | Regenerates the OpenAPI contract and .NET SDK, and checks builder and platform descriptor compatibility. |
| `scenarios` | Drives built binaries as external processes for black-box tests. |
| `sdk-dotnet` | Contains the Kiota-generated .NET client and its end-to-end tests. |

The public API types in `platform/api` are the source for
`api-specifications/openapi.yaml`. The conformance tests generate the OpenAPI
artifacts and then regenerate `sdk-dotnet` from that contract.

## Deployment descriptor

Every built platform binary embeds one `deployment.Descriptor`. It contains the
project, environment, site, machine, role, IP, services, features, and resolved
fabric peers.

The runtime trusts the descriptor as its identity. Registration origins, event
nodes, and fabric members come from it. A client cannot claim a different
machine or site. Runtime JSON configuration contains only site-adjustable
settings: the HTTP listen address, events directory, reconciliation interval,
and fabric socket overrides.

## Runtime boundaries

The platform is composed around three boundaries:

| Boundary | Responsibility |
| --- | --- |
| `internal/httpapi` | Decodes and encodes the public HTTP contract. It does not decide registration state. |
| `internal/registration` | Owns proposals, confirmations, acceptance, reconciliation, and conflict views. It depends only on the fabric contract. |
| `internal/fabric` | Shares named byte collections across the expected machines of one site. Runtime code does not depend on a backend. |

The production fabric adapter is `fabric/olric`. The in-process `fabric/memory`
adapter exists for deterministic tests and runs through the same contract suite.
Olric is imported only by its adapter package.

## Fabric contract

A fabric collection is a named map from string keys to byte values. It supports
create-if-absent, atomic swap, get, and weakly consistent enumeration. There is
no publish, subscribe, request, query, transaction, or index API.

The fabric promises:

- caller-owned byte copies;
- per-key atomic create and swap while membership is stable;
- weakly consistent enumeration;
- fixed expected membership derived from the descriptor;
- separate reachability state: `connected`, `degraded`, or `disconnected`;
- context cancellation and consistent behavior after close.

Expected membership does not shrink when a machine is offline. A one-machine
site is connected by itself. Machines start in any order and merge as peers
become available.

### Membership-change limitation

Olric can briefly violate create-if-absent while a member joins a fabric that
already holds data. During partition movement, a create can report a false win
and overwrite the current value. The stable-membership atomicity contract does
not extend across a join.

Measured on Olric v0.7.4 with two loopback members, 26 or 27 of 40 keys written
before the join produced a false create win in one sub-second polling round.
Reads still found the previous value. Later polling produced no more false wins
over 20 seconds. The affected share matched the partitions moved to the joining
member.

The adapter continues to map create to Olric's `Put` with `NX`. Locks or a
read-back check would have the same AP limitation or could only observe damage
after it occurred. Registration therefore retains immutable contenders and
acceptance evidence, treats single-key current records as repairable views, and
reconciles one winner after membership stabilizes. The two-member Olric
regression reproduces the measured false projection with a focused test hook and
proves the retained state converges.

State is memory-only. The platform does not promise that data survives loss of a
fabric member that owns a partition or a full-site shutdown.

## Bootstrap and lifecycle

Fabric topology is derived, not discovered. The builder resolves each machine's
site peers. The Olric adapter uses topology IPs on fixed ports: `3320` for its
client surface and `3322` for membership. Runtime overrides move sockets for
development and tests but never change machine identity.

Startup order is strict:

1. Open the event sink.
2. Open the fabric.
3. Open registration and complete one reconciliation pass.
4. Start periodic reconciliation.
5. Serve the public HTTP API.

A runtime that cannot open its fabric or finish startup reconciliation does not
serve traffic. Shutdown reverses the ownership order: stop and drain HTTP,
stop reconciliation and finish its active pass, close the fabric, then close the
event sink.

## Domain events

Events are facts recorded by the package that owns the state transition. They
are not log messages. Recording is synchronous and flushed before the emitting
operation returns.

| Package | Events |
| --- | --- |
| `internal/registration` | `platform.registration.requested`, `confirmed`, `accepted`, `rejected`, `conflict` |
| `internal/fabric` | `platform.fabric.started`, `stopped` |

`internal/events` owns only the envelope, recorder, and sink contract. Every
record is self-describing: it carries its own occurrence ID, event type, payload
schema version, occurrence time, source subsystem, and the deployment node that
stated it, plus optional causal links. It deliberately carries no transport
ordering; a shared journal orders events when it accepts them, and that sequence
belongs to the Event Fabric receipt and delivery, not to the immutable fact.

The current JSONL sink writes one file per deployment node and also stamps that
node onto every record, so a future shared journal can pool nodes without losing
attribution. An empty `events_dir` uses a no-op recorder. Event storage is for
development and scenarios; it is not yet a durable replay mechanism or the
authoritative conflict query.

## Event Fabric migration (accepted decisions)

The platform is migrating from the shared Olric fabric to an event-driven model
in which a NATS JetStream site journal is the source of truth and every node
rebuilds its own projections from it. The full plan is in `docs/plan`. Stage 1
froze the target contract and Stage 2 built the NATS JetStream adapter
(`internal/eventfabric/nats`) that implements it; both are tested in isolation.
Stage 3 added event-backed command and query services, strict local projection
replay, and durable registration handlers in
`internal/registration/eventmodel`.
The Olric fabric and the JSONL recorder described above remain the live
mechanisms until the runtime cutover (Stage 4) composes the adapter in.

The accepted decisions this stage records:

- The OPDL Event Fabric (`internal/eventfabric`) is the only coordination
  boundary domains use. It exposes publish, replay, live delivery, health, and
  shutdown, and it hides every transport concept: no domain constructs a
  subject, stream, or consumer.
- One ordered journal per site is the common order for every projection. A route
  is `opdl.<site-scope>.event.<domain>.<fact>`, where `site-scope` is a stable
  transport-safe hash of the length-prefixed project, environment, and site. The
  journal is named `OPDL_<UPPER_SITE_SCOPE>_EVENTS` and binds
  `opdl.<site-scope>.event.>`. It uses file storage and rejects new events when
  a configured limit is reached rather than deleting replay history.
- Delivery is at least once. Every projector and handler is idempotent by event
  identity; transport deduplication is an optimization, never correctness. A
  malformed or unsupported event stops catch-up and makes the node unready
  rather than being skipped.
- Startup runs one continuous ordered consumer from the first retained event,
  waits until the projector has applied the captured high-water sequence, and
  keeps the same consumer for live delivery, so there is no replay-to-live gap.
- Registration becomes a projection of `proposed`, `confirmed`, `rejected`, and
  `accepted` events (`internal/registration/eventmodel`). See
  `docs/02-registration.md` for the event-sourced target.

## No redundancy

There is one platform and one fabric member per machine. OPDL currently has no
primary/secondary instance, election, fencing, failover, or zero-downtime
upgrade mechanism. Backend partitioning or replication must not be interpreted
as service redundancy.

