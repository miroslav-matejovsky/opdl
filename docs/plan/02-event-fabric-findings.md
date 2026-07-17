# Stage 2 findings: remaining work and decisions

Findings resolved by Stage 3 were removed. Registration projections now ignore
events outside their domain while rejecting unknown registration events, and
all registration facts provide stable Event Fabric deduplication identities.

## 1. Catch-up orchestration remains runtime-owned

The adapter exposes `HighWater`, `State`, and a continuous `RunProjector`. It
does not run the bounded startup loop that captures high water and waits for the
projection to apply it. Stage 4 owns that orchestration and is where
`eventfabric.ErrCatchUpTimeout` must be returned.

## 2. Multi-node clustering is configured but not integration-tested

Storage selection, replica count, routes, and non-storage journal waiting are
implemented and unit-tested. Real adapter tests still use one embedded storage
node. Before the runtime cutover relies on a two-node site, add a simultaneous
test where one storage node and one Core NATS node both publish and replay.

## 3. Pinned NATS versions favor the mature line

The adapter uses `nats-server/v2 v2.11.9` and `nats.go v1.46.1`. These versions
support the embedded server and modern JetStream APIs used here. A version bump
should be a separate dependency change with the adapter integration tests run
against it.

## 4. Monitoring binds on every node

Every embedded server binds its monitoring address, including a single-node
site. This keeps one startup path. Stage 4 may expose an option to disable it if
an unused listener is undesirable.

## 5. The adapter is not composed into the runtime

The platform still runs Olric, reconciliation, and JSONL. Stage 4 must derive
NATS configuration, run projection and handler loops, gate HTTP on catch-up, and
own their shutdown before Stage 5 can delete the old fabric.
