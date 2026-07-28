# Site level

A site is the set of machines under one blueprint site. Site facts must converge
across those machines.

## Packages

| Package | Responsibility | Current state |
| --- | --- | --- |
| `eventfabric` | Ordered site delivery and durable-consumer contract. | Contract and tests only |
| `registration` | Registration events, projection, commands, queries, and handler. | Projection implemented; command and handler are stubs |

## Event Fabric

`eventfabric.Delivery` adds a site sequence to an immutable event envelope.
`Consumer.Follow` replays unacknowledged deliveries and then follows live.
`Consumer.Ack` persists progress for a machine-scoped consumer identity.
Delivery is at least once.

Publication uses the shared `events.Publisher`; `eventfabric` does not define a
second event or publisher model. A concrete site adapter must accept only
site-scoped envelopes, assign the site order, and implement durable consumption.
No adapter exists yet.

## Registration

Registration declares four site-scoped facts: proposed, confirmed, rejected,
and accepted. The projection folds ordered deliveries and uses deterministic
proposal and decision identities. The first proposal for a unit key by site
sequence owns that key.

`CommandService.Create`, `NewHandler`, and runtime site composition are not
implemented. `app.topology` currently returns only the local machine. The public
registration API therefore returns `503`.

The target protocol is documented in
[Registration](../../../docs/03-registration.md). Remaining work is in the
[hierarchy plan](../../../docs/plans/hierarchy/README.md).
