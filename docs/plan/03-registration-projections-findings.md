# Stage 3 findings: registration projections

Findings and stage-boundary constraints discovered while implementing
[Stage 3](03-registration-projections.md). The implemented decisions below are
still in force; the package they describe now *is* `internal/registration`.

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

## Resolved by Stage 4

The cutover dependency this file recorded is done: `eventmodel` was promoted into
`internal/registration`, the Olric-backed registration files are deleted, and the
HTTP boundary serves the asynchronous contract. The open question about
expected-machine IPs was decided in favour of an empty IP for a historical
machine; see [04-runtime-cutover-findings.md](04-runtime-cutover-findings.md).
