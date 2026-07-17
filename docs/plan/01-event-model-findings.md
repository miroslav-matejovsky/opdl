# Stage 1 findings: remaining decisions

Findings already resolved by Stages 2, 3, and 4 were removed. This file contains
only decisions or deferred work that still affects later stages.

## 1. Node identity is a nested envelope object

Project, environment, site, machine, and role are modeled as one `node` object
that reuses `events.Node`. This keeps identity cohesive and avoids repeating
five top-level fields. External journal readers must treat this nested shape as
the wire contract.

## 2. Schema version identifies the payload schema

`schema_version` is declared by each event payload. It is not an envelope-format
version. This lets one event type evolve independently and lets replay stop when
it encounters a payload encoding it does not understand.

## 3. Event types and routes use different namespaces

Event types use `platform.<domain>.<fact>`. Event Fabric routes use
`opdl.<site-scope>.event.<domain>.<fact>`. The distinction is intentional: the
first is a product event contract and the second is a transport-safe OPDL route.

## 4. Rejected claims do not release a unit key

The first proposal in journal order permanently claims its unit key. A rejected
claim remains visible and the key stays claimed. Registration removal or retry
after rejection needs a future explicit release event. It must not be added as
implicit cleanup during this migration.

