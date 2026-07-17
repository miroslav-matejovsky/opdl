# Stage 1: Event model and decisions

## Outcome

Define the target behavior before introducing NATS. This stage fixes the event
envelope, routing scope, ordering rules, delivery guarantees, and registration
workflow that later stages implement.

Complexity: Medium.

Estimated time: 2-3 engineering days.

## Work

1. Write the Event Fabric contract in `platform/internal/eventfabric/doc.go`.
   Keep the contract about OPDL behavior, not NATS APIs.
2. Define the shared event envelope with:
   - event ID and event type;
   - schema version;
   - occurrence time;
   - project, environment, site, machine, and role identity;
   - causation ID and optional correlation ID;
   - immutable JSON payload.
3. Remove process-local sequence as a cross-node ordering promise. The journal
   receipt and delivery metadata provide the site stream sequence.
4. Define deterministic route construction. A route is scoped by project,
   environment, and site, then by domain and event type. Deployment names must
   be safely encoded and validated by Event Fabric, never concatenated by a
   domain package.
5. Define one limits-retained site journal. Use file storage and reject new
   events when limits are reached so required replay history is not silently
   removed.
6. Define at-least-once handling:
   - publish returns only after JetStream acknowledges durable acceptance;
   - a handler acknowledges only after its state change or resulting event is
     complete;
   - projection application is idempotent by event ID and domain identity;
   - malformed or unsupported events stop catch-up and make the node unready.
7. Define startup catch-up. Capture the journal high-water sequence, replay
   through it, switch to live delivery without a gap, then allow the HTTP API to
   serve.
8. Replace the current registration storage algorithm with this event flow:
   - the origin publishes `platform.registration.proposed` after validating the
     HTTP input;
   - every expected node handles the proposal and publishes its own confirmed
     or rejected decision;
   - the origin publishes accepted after every expected node confirmed;
   - the first valid proposal for a unit key in site journal order is the
     winner; later different proposals are rejected as conflicts;
   - all query views are deterministic projections of these events.
9. Give handler outcomes stable decision identities. Redelivery may try to
   publish the same outcome again, so projectors must collapse it even after the
   NATS duplicate window has expired.
10. Update the architecture and registration documentation with the accepted
    decisions before implementing the adapter.

## Deliverables

- Package documentation for Event Fabric and the event envelope.
- A registration event catalog with payloads and invariants.
- A route and journal naming specification.
- A short lifecycle specification for publish, replay, live handling, failure,
  and shutdown.
- Unit tests for envelope validation, route encoding, event decoding, and pure
  registration projection rules.

## Exit criteria

- The team can trace a registration from HTTP proposal through confirmation,
  acceptance or rejection, and every query projection without referring to a
  distributed map.
- Ordering and duplicate-delivery outcomes are deterministic.
- Every required Event Fabric guarantee has an adapter contract test planned.
- The NATS deployment and stream replication choice is recorded.

## Open questions

- Should the POC keep an embedded NATS server per OPDL node, as proposed, or run
  a separately managed site cluster? Embedded servers keep packaging close to
  the current model. A managed cluster separates runtime and storage failure.
- What JetStream replica count should a one-node, two-node, and three-or-more
  node site use? The simple POC default is one replica. Two replicas do not
  provide useful failure tolerance because both are needed for quorum.
- Is acceptance still required from every expected node, or should registration
  use a quorum? The plan retains the existing all-node rule until changed
  explicitly.
- How much event history must be retained? Full replay requires either retained
  history or a later snapshot mechanism.
- Should a valid POST always return `202` with a proposal ID, leaving conflict
  resolution to the projection? This is the cleanest asynchronous contract and
  intentionally breaks the current immediate `409` behavior.

## Risks

- An incomplete event payload can make projections depend on queries or hidden
  state. Require self-contained events and test replay from an empty projection.
- Treating NATS deduplication as exactly-once processing can create duplicate
  decisions after its window expires. Correctness must live in idempotent domain
  rules.
- A site journal gives a common order, but that order is when NATS accepted the
  event, not when a client began its request. Document this as the conflict rule.
- Retention limits can make a full replay impossible. Rejecting new events is
  safer than silently deleting old history, but it can stop writes when disk is
  full.
- Two-node JetStream topology can appear redundant while still losing
  availability on one failure. Make this explicit in deployment documentation.

