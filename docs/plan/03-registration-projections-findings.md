# Stage 3 findings: registration projections

Findings and stage-boundary constraints discovered while implementing
[Stage 3](03-registration-projections.md).

## Implemented decisions

### 1. One projection filters domains and stays strict within registration

The registration projection advances its applied sequence for non-registration
events, including Event Fabric lifecycle events. An unknown
`platform.registration.*` type, unsupported schema version, invalid identity,
or inconsistent decision stops the projection. This resolves the Stage 2
lifecycle-event collision without adding a generic dispatcher.

### 2. Domain identities are also transport deduplication identities

Proposals, decisions, and acceptances implement `eventfabric.Identified` with
event-kind-prefixed IDs. The prefix prevents two event kinds with the same raw
domain identifier from colliding in the site-wide JetStream deduplication scope.
Projection idempotency remains the correctness mechanism after the NATS
deduplication window expires.

### 3. Handler publications carry causal links

`eventfabric.CausalContext` carries an input delivery into a resulting publish.
The NATS adapter stamps the input event ID as `causation_id` and starts or
continues `correlation_id`. A command-created proposal has no cause. This
resolves the Stage 1 causal-envelope deferral.

### 4. Structurally invalid history stops; semantically invalid proposals reject

A proposal with a forged ID, impossible event order, unexpected deciding
machine, or contradictory decisions is invalid journal history and stops
replay. A structurally coherent proposal that fails current registration input
or trusted-topology validation is projected and receives a deterministic
`registration_invalid_proposal` rejection from each expected handler.

## Runtime cutover dependency

### 5. Promotion and public API activation must happen with Stage 4

The new command service, query service, projection, and handler are complete in
`internal/registration/eventmodel`, with direct tests and no dependency on NATS
or shared state. They are not yet used by `internal/app` or `internal/httpapi`.

Moving them into `internal/registration` now would collide with the live event
types and leave the platform without a compilable runtime unless Stage 4 also
added NATS configuration, startup replay, handler lifecycle, readiness, and
shutdown. Therefore Stage 4 must perform one breaking cutover:

1. Compose NATS, the projection, and the durable handler.
2. Open the event-backed command and query services.
3. Change POST to return `proposal_id` and journal sequence.
4. Change status lookup to use proposal ID and return journal-unavailable as
   `503`.
5. Regenerate OpenAPI and the .NET SDK and update black-box scenarios.
6. Delete the Olric-backed registration files and promote `eventmodel` into the
   parent package.

This is not a compatibility requirement. It is an atomic build and lifecycle
boundary. No dual write or state import is needed.

## Remaining design decision

### 6. Query views obtain expected-machine IPs from the current descriptor

`Proposed` captures expected machine names, as the accepted event contract
requires, but does not capture every expected machine IP. Query views map those
names to IPs from the node's trusted descriptor. A historical proposal with a
machine no longer present can still be reconstructed, but that instance has no
IP in the public view.

Recommendation: keep names as the acceptance identity and do not expand the
proposal solely for a display field. Stage 4 should either allow an empty IP for
historical machines or remove per-instance IP from the new HTTP contract.
