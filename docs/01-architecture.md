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
project, environment, site, machine, role, IP, services, features, explicit
primary and optional secondary platform endpoints, and resolved fabric peers.
The descriptor is the deployment source of truth. Production code defines no
default API or fabric ports.

Blueprint authors may use this allocation for consistency, but the builder and
runtime never apply it implicitly:

| Instance | HTTP API | Olric client | Olric memberlist |
| --- | ---: | ---: | ---: |
| Primary | 8080 | 3320 | 3322 |
| Secondary | 8081 | 3321 | 3323 |

Every enabled instance must state all three complete host:port addresses in the
blueprint. The resolved descriptor carries those exact values.

The runtime trusts the descriptor as its identity. Registration origins, event
nodes, and fabric members come from it. A client cannot claim a different
machine or site. Runtime JSON configuration contains only site-adjustable
settings: controlled per-instance socket overrides, the events directory,
reconciliation interval, and lifecycle timeouts. Socket overrides are an
operational fallback for exceptional production fixes, not a second topology.

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

Fabric topology is derived, not discovered. The builder resolves every
machine's explicit platform instance endpoints and site peers. The Olric adapter
reads those endpoints from the descriptor. Runtime overrides can move one named
instance's sockets for an exceptional production fix, development, or tests,
but never change deployment identity.

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

`internal/events` owns only the envelope, recorder, and sink contract. The
current JSONL sink writes one file per deployment node. An empty `events_dir`
uses a no-op recorder. Event storage is for development and scenarios; it is not
a durable replay mechanism or the authoritative conflict query.

## No redundancy

The descriptor now models primary and optional secondary platform instances,
with secondary enabled by default in authored topology. The current runtime
still launches its primary process only. Later implementation stages add
instance-aware fabric membership, concurrent process lifecycle, SDK failover,
and rolling upgrades. Backend partitioning or replication must not be
interpreted as completed platform redundancy.

