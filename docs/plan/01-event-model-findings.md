# Stage 1 findings: remaining decisions

Findings already resolved by Stages 2 and 3 were removed. This file contains
only decisions or deferred work that still affects later stages.

## 1. Event model package promotion depends on the runtime cutover

The event catalog, projection, command and query services, and durable handler
remain in `platform/internal/registration/eventmodel`. The live
`platform/internal/registration` package still declares Olric event types with
the same Go names, so the event model cannot move into that package before the
live store and reconciler are removed.

Resolution needed in Stage 4: compose the Event Fabric and event-backed services
in the runtime, update the HTTP boundary, delete the old registration files, and
then move `eventmodel` into `registration`. Do this as one cutover. A forwarding
or compatibility package would add no value during the POC.

## 2. Node identity is a nested envelope object

Project, environment, site, machine, and role are modeled as one `node` object
that reuses `events.Node`. This keeps identity cohesive and avoids repeating
five top-level fields. External journal readers must treat this nested shape as
the wire contract.

## 3. Schema version identifies the payload schema

`schema_version` is declared by each event payload. It is not an envelope-format
version. This lets one event type evolve independently and lets replay stop when
it encounters a payload encoding it does not understand.

## 4. Event types and routes use different namespaces

Event types use `platform.<domain>.<fact>`. Event Fabric routes use
`opdl.<site-scope>.event.<domain>.<fact>`. The distinction is intentional: the
first is a product event contract and the second is a transport-safe OPDL route.

## 5. Rejected claims do not release a unit key

The first proposal in journal order permanently claims its unit key. A rejected
claim remains visible and the key stays claimed. Registration removal or retry
after rejection needs a future explicit release event. It must not be added as
implicit cleanup during this migration.

## 6. JSONL repeats node identity

The JSONL filename and each event envelope both identify the node. Stage 4
removes JSONL as a second event path, so no new behavior should depend on the
filename carrying identity.
