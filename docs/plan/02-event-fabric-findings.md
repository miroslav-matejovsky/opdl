# Stage 2 findings: discovered issues and inconsistencies

Unknowns, deferrals, and decisions surfaced while implementing
[Stage 2](02-event-fabric.md). Recorded for analysis before the runtime cutover
(Stage 4) and registration projections (Stage 3) build on the adapter.

## Design gaps to resolve in a later stage

### 1. Fabric lifecycle events collide with a strict domain projector

The adapter publishes `platform.event_fabric.ready` and
`platform.event_fabric.stopping` into the same journal (Stage 2 work item 12).
The node-wide projector replays the whole journal, so any projector wired to it
receives these lifecycle events. The Stage 1 `eventmodel.Projection` returns an
error on any event type it does not recognize, by design, to stop catch-up on a
malformed registration event. Wired as the node-wide projector in Stage 3/4, it
would error on the fabric's own lifecycle events.

Resolution needed in Stage 3: either the node-wide projector routes deliveries to
domain projectors by domain token and only a matching domain projector sees an
event, or a domain projection ignores events outside its domain
(`platform.<other>.*`) while still stopping on a malformed event inside its own.
The event type already carries the domain, so either is cheap. The integration
tests here use recording projectors that accept every event, so nothing exercised
the strict path yet.

### 2. Catch-up orchestration and `ErrCatchUpTimeout` are not yet produced

The adapter exposes `HighWater`, `State` (with `Applied`/`CaughtUp`), and a
`RunProjector` that replays from the first event and continues live. It does not
itself run the "capture high-water, wait until the projector applies it, bounded
by the catch-up timeout" loop. That orchestration belongs to runtime composition
(Stage 4), which will start the projector, read `HighWater`, and poll `State`
until `CaughtUp` or the bound elapses, returning `eventfabric.ErrCatchUpTimeout`.
The error value is defined now as part of the documented error contract (work
item 11) but is not produced by the adapter; Stage 4 produces it. This is a
deliberate split, not an omission.

### 3. `Identified` dedup keys are not yet on domain events

Publish deduplicates by `eventfabric.Identified.DedupID()` when an event
implements it, else by the unique envelope ID. The registration events in
`eventmodel` do not implement `Identified` yet; Stage 3 makes them return their
`proposal_id` or `decision_id`, so a handler that republishes a decision after
redelivery collapses onto one journal entry. Until then no handler republishes,
so the gap has no effect. The integration test uses a local `keyedProbe` to prove
the mechanism.

## Deferrals

### 4. Multi-node cluster is derived and configured but tested single-node

Storage-node selection, replica count, cluster name, and peer routes are derived
from the descriptor and unit-tested, and the embedded server is configured with a
cluster listener and routes when the site has peers. The integration tests,
however, run a single embedded storage node — the POC topology for one- and
two-node sites, where exactly one node hosts JetStream. The following are not yet
exercised against real servers and are deferred to a focused multi-server test:

- a two-node site where one node runs JetStream storage and the other runs Core
  NATS and routes to it, with both able to publish;
- a genuine two-server "publish on one node, replay on another" test.

The restart-replay test covers durability and replay across server instances over
the same journal, and the exit criterion for cross-instance replay is met in that
weaker form. A true simultaneous two-node cluster test is the highest-value
remaining Stage 2 test and the most prone to timing flakiness; it should be added
before the runtime cutover relies on multi-node sites. The
`awaitJournal` path (a non-storage node waiting for a storage node to create the
journal) is implemented and statically reachable but only meaningful once that
multi-server test exists.

## Decisions worth confirming

### 5. Pinned versions favor a mature line over the newest

The plan recommends the newest stable `nats-server/v2` and `nats.go`. The newest
at implementation time were around `nats-server/v2 v2.14.x` and
`nats.go v1.52.x`; this stage pinned the more mature, widely deployed
`nats-server/v2 v2.11.9` and `nats.go v1.46.1`, which build cleanly on the
toolchain and expose the same embedded-server and modern `jetstream` APIs. If the
project prefers strictly-newest, bumping both and re-running the adapter tests is
low risk. The versions are recorded in the adapter's package documentation.

### 6. Monitoring binds a port on every node, including single-node

`serverOptions` always configures the monitoring listener (loopback by default).
A single-node site therefore binds a monitoring port it may never be scraped on.
This is harmless and keeps one code path; a future option could disable
monitoring when its address is unset.

### 7. The adapter is not wired into the runtime

Stage 2 builds and tests the adapter in isolation, as the plan intends. The
platform still runs on Olric and the JSONL recorder. Composing the adapter into
`internal/app` — deriving its `Config`, running the projector and handlers,
gating HTTP readiness on catch-up, and removing the JSONL second source of truth
— is the runtime cutover in [Stage 4](04-runtime-cutover.md).
