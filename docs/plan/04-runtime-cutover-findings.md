# Stage 4 findings: runtime cutover

Findings and decisions discovered while implementing
[Stage 4](04-runtime-cutover.md).

## Corrected decisions

### 1. The site's NATS cluster is exactly its storage nodes

The accepted topology said other OPDL nodes "still run Core NATS and route
JetStream requests to the storage nodes". Measured against NATS 2.11.9, that
does not work, and the plan's own reason for the topology is what breaks.

NATS sizes a JetStream metadata group from the server's configured routes, not
from the servers that actually enable JetStream (`server/raft.go`, "Determining
expected peer size for JetStream meta group": the expected peer count is
`max(2, len(opts.Routes) + gateways)`). A two-machine site with one storage node
therefore expects two metadata peers, only ever has one, never elects a metadata
leader, and every JetStream API call times out. This was reproduced in isolation
both with the second machine down and with it running: a Core NATS node in the
cluster does not make the group electable, it makes it inquorate.

The rule is now: only a storage node runs a NATS server, and the cluster is
exactly the storage nodes. Every other machine of the site runs no server and
connects to the storage nodes as a client. A single-storage site runs one
unclustered server, which is what lets a two-machine site keep working while its
second machine is down — the property the original topology was chosen for.

This also removes the Stage 2 finding that monitoring binds on every node: only
a storage node binds anything.

### 2. Readiness is stated by composition, not by the adapter

Stage 2's adapter published `platform.event_fabric.ready` when its connection
opened, and `stopping` from `Close`. Stage 4 moved both out.

Readiness is a conclusion about the whole node — its journal, its projections,
and its handlers — and a transport can only report on itself. `Open` now states
nothing and returns a fabric that is usable but not ready; composition runs the
readiness sequence and states the fact. `Close` states nothing either: a node
announces its shutdown while the journal can still accept it, which is also what
work item 5 means by not claiming a completed close through the transport being
closed.

`eventfabric.Ready` gained the server, journal, storage role, and replica count
via `eventfabric.Info`, which the adapter reports and composition publishes.

### 3. Per-instance IPs may be empty for a historical proposal

Resolving Stage 3's open question: the HTTP contract keeps `ip` on each platform
instance, and it is empty when the answering node's descriptor no longer has
that machine. The proposal carries machine names as the acceptance identity, and
expanding it to carry every expected IP for a display field would change what a
proposal *is* for the sake of a rendering detail.

## Discovered and fixed

### 4. A cancelled loop reported its cancellation as a projector failure

`RunProjector` and `RunHandler` wrapped any `Apply`/`Handle` error, including the
`context.Canceled` an in-flight delivery returns when shutdown cancels the loop.
Every shutdown that caught a delivery mid-flight was reported as a node that had
broken. The adapter's consume loop now treats an error raised under a cancelled
context as a stop, not a verdict on the event.

## Deferred with reasons

### 5. Graceful shutdown has no black-box scenario

Work item 9 asks for one. A portable graceful interrupt of a child process does
not exist on Windows, which is this repository's development platform, so the
scenario harness force-stops machines. The shutdown ordering, the stopping
event, and the release of every dependency are covered in-process by
`internal/app`'s tests, and the force stop is the more demanding test of what a
scenario can actually observe: the platform must come back from a kill, which
`TestRestartRebuildsStateFromTheJournal` asserts.

### 6. A journal that fills has no scenario

Work item 9 asks for "NATS startup failure", which
`TestPlatformRefusesToStartWithoutItsJournalStorage` covers by making the data
directory unusable. The related failure — a journal at its byte limit rejecting
new events with `DiscardNew` — is configured and not exercised. Filling a 1 GiB
journal in a scenario is not worth the runtime; a focused adapter test with a
small `MaxBytes` would be, and is not written.

### 7. One projection carries every domain

The node-wide projector is the registration projection, which advances its
sequence for events outside its domain (Stage 3 finding 1). That is exactly one
domain, so no dispatcher exists. A second stateful domain needs one: either a
projector that fans a delivery out to domain projections, or a second ordered
consumer. Composition already runs projectors as a list, so this is a change to
`site.start`, not to the Event Fabric contract.

### 8. Readiness reports nothing over HTTP

The plan's open questions recommend an OPDL readiness response carrying journal
state, projector applied/high-water sequence, handler pending counts, and the
last error. It is not built. The information all exists — `Fabric.State`,
`Fabric.HandlerPending`, and `Projection.Sequence` are what the startup sequence
gates on, and a failed startup already reports them in its error — but nothing
exposes it after the node is serving.

What the platform does promise is stronger than a health endpoint would be: a
node does not serve until it has caught up, so an answer on the registration API
already means the projection was current at startup. What is missing is live lag:
a node whose projector dies stops serving (`serve` watches its loops), but a node
whose projector merely falls behind keeps answering from a stale view. Bounding
that needs the readiness response.

### 9. A non-storage machine cannot start before its storage node

A machine that does not store the journal waits for a storage node's server to
accept its connection, bounded by `startup_timeout`, and then waits for the
journal to exist, bounded again. If no storage node is running, it does not
start.

This is inherent to a site with one storage node and is not new — the journal is
the only place its state can come from. It does mean site boot order matters for
a two-machine POC site: the storage node is the first machine by sorted name, and
it has to be up. A three-machine site tolerates one storage node being down.
