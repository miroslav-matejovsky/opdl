# Site level

A site is the set of machines under one blueprint site. Site facts must converge
across those machines.

## Packages

| Package | Responsibility | Current state |
| --- | --- | --- |
| `eventfabric` | Ordered site delivery contract, and the client onto this instance's server. | Contract, plus a NATS client with a health check |
| `healthfabric` | Ephemeral service-health publication and subscription on Core NATS. | Implemented |
| `healthview` | Static service inventory, observer slots, freshness, and deterministic reduction. | Implemented |

The site's transport exists: every deployed instance at a site runs an embedded
NATS server and all of them form one cluster. What does not exist yet is the
ordered, durable delivery the contract below describes.

Service health uses the same cluster through a separate connection. It is not
part of the durable event contract: observations are bounded current-state
snapshots with no replay or result persistence. Every receiver rebuilds its
view from static inventory and fresh traffic.

## Event Fabric

`eventfabric.Delivery` adds a site sequence to an immutable event envelope.
`Consumer.Follow` replays unacknowledged deliveries and then follows live.
`Consumer.Ack` persists progress for a machine-scoped consumer identity.
Delivery is at least once.

Publication uses the shared `events.Publisher`; `eventfabric` does not define a
second event or publisher model. A concrete site adapter must accept only
site-scoped envelopes, assign the site order, and implement durable consumption.
No adapter exists yet.

## Client

`eventfabric.Client` is the connection this instance holds to its own embedded
NATS server, which is a member of the site's cluster. The server is
instance-level and lives in
[`internal/instance/natsserver`](../instance/README.md); the client is here
because reaching the site is a site concern. It takes the server as an
`InProcessConnProvider`, a local interface naming the one capability it needs,
so the site level never imports the instance level's package.

`Client.Check` publishes a message to a subject of its own and reads it back. It
is what `/health` reports the fabric on: a client can call itself connected to a
server that has stopped delivering, and the question is whether the fabric
works. It is a round trip through this instance's own member, not across the
cluster, so it says this instance's half of the fabric is alive. A failing check
makes the instance Degraded rather than Unhealthy — Unhealthy is the gate a
Passive instance promotes through, and moving Primary Ownership does not fix a
broken fabric, because the other instance runs its own server.

`Client` does not implement `Consumer`. The deployment runs NATS without
JetStream, so there is no durable order to replay or acknowledge. That is what
`Consumer` will need.

Dynamic unit registration previously lived at this level and has been removed.
Current unit membership is static. The older
[hierarchy plan](../../../docs/plans/hierarchy/README.md) is superseded except
for its still-open durable site-distribution problem.
