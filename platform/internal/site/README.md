# Site level

A site is the set of machines under one blueprint site. Site facts must converge
across those machines.

## Packages

| Package | Responsibility | Current state |
| --- | --- | --- |
| `eventfabric` | Ordered site delivery and durable-consumer contract. | Contract and tests only |

## Event Fabric

`eventfabric.Delivery` adds a site sequence to an immutable event envelope.
`Consumer.Follow` replays unacknowledged deliveries and then follows live.
`Consumer.Ack` persists progress for a machine-scoped consumer identity.
Delivery is at least once.

Publication uses the shared `events.Publisher`; `eventfabric` does not define a
second event or publisher model. A concrete site adapter must accept only
site-scoped envelopes, assign the site order, and implement durable consumption.
No adapter exists yet.

Dynamic unit registration previously lived at this level and has been removed;
unit membership is moving to the static Site → Machine → Instance approach
described in the [hierarchy plan](../../../docs/plans/hierarchy/README.md).
