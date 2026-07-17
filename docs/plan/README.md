# NATS event architecture migration

Status: proposed for the POC.

This plan replaces the shared Olric collections with an event-driven model.
NATS JetStream becomes the durable site event journal. Each platform node
rebuilds its own local projections from that journal and uses those projections
to answer queries. No service reads or writes distributed maps.

Backward compatibility and migration of existing in-memory state are out of
scope. The cutover may change configuration, event contracts, HTTP response
details, tests, and deployment descriptors.

## Objectives

- Make immutable events the source of truth for service coordination.
- Replace Olric with NATS and JetStream.
- Put an OPDL-specific Event Fabric API in front of NATS.
- Keep NATS subjects, stream names, consumers, acknowledgements, and retry
  details out of domain packages.
- Build node-local state with deterministic, idempotent projections.
- Support replay after restart and delivery to nodes that were offline.
- Remove Olric, the shared in-memory fabric, periodic distributed-state scans,
  and the in-memory fabric test adapter.

## Target architecture

```text
external client
      |
      v
HTTP command/query API on one OPDL node
      |                         ^
      | publish                 | query
      v                         |
OPDL Event Fabric          local projections
      |                         ^
      v                         | replay and live events
NATS JetStream site journal ----+
      |
      +--> durable node handlers --> resulting events
```

HTTP remains the external API. Communication and coordination between OPDL
services goes only through the Event Fabric.

## Core decisions

- Use JetStream, not Core NATS alone. A node must receive events published while
  it was offline and must be able to rebuild a projection from retained history.
- Keep one ordered journal per site. Journal order is the common ordering used
  by every projection. The first published proposal claims a registration key;
  a later different proposal is a conflict.
- Use limits-based retention with file storage. Reaching a configured limit
  must reject new events instead of silently deleting history needed for replay.
- Treat delivery as at least once. Event handlers and projections must be
  idempotent. NATS message deduplication is an optimization, not correctness.
- Carry deployment node identity in every event envelope. The current JSONL
  convention of keeping node identity only in the filename cannot work in a
  shared journal.
- Start with one embedded NATS server per OPDL node and derive its site cluster
  peers from the deployment descriptor.
- Enable JetStream on one deterministic storage node in one-node and two-node
  POC sites. Enable it on three deterministic storage nodes when a site has at
  least three nodes. Use one and three stream replicas respectively. Other OPDL
  nodes still run Core NATS and route JetStream requests to the storage nodes.
  This avoids a two-member JetStream metadata group that loses quorum when one
  member fails.
- Rebuild in-memory projections from the journal at process start for the POC.
  Local projection persistence and snapshots are deferred until replay cost
  proves they are needed.
- Do not dual-write to Olric and NATS. Each use case moves directly to events,
  then the obsolete state fabric is deleted.

## Recommended POC baseline

Use these defaults unless a stage records a different decision:

| Area | Recommendation |
| --- | --- |
| Server | Link the NATS server into the platform process. Run one server per OPDL node. |
| Site topology | Keep peer identities and IPs in the descriptor. Use fixed NATS ports with scenario-only overrides. |
| Journal | One file-backed, limits-retained stream per site with `DiscardNew`. |
| Storage and replicas | One JetStream node and replica for sites smaller than three nodes; three JetStream nodes and replicas otherwise. Select storage nodes by sorted machine name for the POC. |
| History | No age or message-count deletion before snapshots exist. Configure a byte limit and fail writes when full. |
| Delivery | At least once. Durable pull consumers for coordination. One node-wide continuous ordered consumer dispatches to local projectors. |
| Registration acceptance | Require every expected machine, as today. Capture that machine set in the proposal event. |
| HTTP proposal result | Return `202` plus a proposal ID after durable publish. Resolve conflicts asynchronously. |
| Projection storage | In memory, rebuilt from the complete retained journal on every start. |
| Security | Require site credentials for non-loopback deployments. Allow unauthenticated loopback only in tests. |
| Compatibility | No dual write, old-state import, forwarding package, or compatibility configuration. |

## OPDL Event Fabric vocabulary

| OPDL term | Meaning | NATS implementation detail |
| --- | --- | --- |
| Event Fabric | Runtime boundary for publishing, replay, handling, health, and shutdown | NATS connection and JetStream context |
| Journal | Ordered, retained event history for one site | JetStream stream |
| Route | Stable OPDL destination derived from deployment scope and event type | NATS subject |
| Projector | Replays events and maintains one node-local query model | Ordered replay followed by live consumption |
| Handler | Reacts to selected events for one service on one node | Durable pull consumer with explicit acknowledgement |
| Receipt | Proof that the journal accepted an event | JetStream publish acknowledgement and stream sequence |

Domain packages use OPDL event types, projectors, and narrow publisher or
handler interfaces. They do not construct subjects or configure NATS resources.

The initial route format should be
`opdl.<site-scope>.event.<domain>.<fact>`. `site-scope` is a stable, safe token
derived from project, environment, and site. Human-readable deployment identity
stays in the event envelope. Use one stream named from the same scope and bind it
to `opdl.<site-scope>.event.>`.

## Stages

Estimates are for one engineer and include code, tests, and documentation. They
assume the existing registration flow is the only stateful use case being moved.

| Stage | Outcome | Complexity | Estimate | Status |
| --- | --- | --- | --- | --- |
| [1. Event model and decisions](01-event-model.md) | Freeze the event, ordering, topology, and projection rules | Medium | 2-3 days | Complete |
| [2. OPDL Event Fabric](02-event-fabric.md) | Add the OPDL abstraction and NATS JetStream adapter | High | 4-6 days | Not started |
| [3. Registration projections](03-registration-projections.md) | Replace shared registration collections and scans with events | High | 5-8 days | Not started |
| [4. Runtime cutover](04-runtime-cutover.md) | Run the platform and scenarios solely through NATS | High | 3-5 days | Not started |
| [5. Remove distributed state](05-remove-distributed-state.md) | Delete Olric, memory fabric, and obsolete paths | Medium | 2-4 days | Not started |

Total estimate: 16-26 engineering days.

Stage 1 froze the contract in code: the event envelope, the OPDL Event Fabric
package (`platform/internal/eventfabric`), the route and journal naming, and the
event-sourced registration model (`platform/internal/registration/eventmodel`),
all unit-tested. NATS is not yet wired and the Olric registration runtime is
unchanged. Issues surfaced while implementing it are in
[01-event-model-findings.md](01-event-model-findings.md).

## Completion criteria

- NATS JetStream is the only communication and coordination mechanism between
  services.
- Every node can rebuild its registration views from the retained site journal.
- A node that was offline consumes missed events when it returns.
- Duplicate delivery does not change a projection or emit duplicate decisions.
- No runtime or test depends on `fabric.Collection`, Olric, or a shared
  in-memory fabric.
- The Olric dependency, adapter, configuration, tests, and documentation are
  removed.
- The periodic registration reconciler and its collection scans are removed.
- Startup does not serve the HTTP API until NATS is ready and local projections
  have caught up to a recorded journal high-water mark.
- `task all` passes.

## NATS references

- [JetStream overview](https://docs.nats.io/nats-concepts/jetstream)
- [Streams and retention](https://docs.nats.io/nats-concepts/jetstream/streams)
- [Consumers and delivery](https://docs.nats.io/nats-concepts/jetstream/consumers)
- [JetStream clustering](https://docs.nats.io/running-a-nats-service/configuration/clustering/jetstream_clustering)
