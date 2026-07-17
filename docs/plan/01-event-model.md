# Stage 1: Event model and decisions

## Outcome

Define the target behavior before introducing NATS. This stage fixes the event
envelope, routing scope, ordering rules, delivery guarantees, and registration
workflow that later stages implement.

Complexity: Medium.

Estimated time: 2-3 engineering days.

## Work

1. Create `platform/internal/eventfabric/doc.go`. Specify `Fabric`, `Publisher`,
   `Receipt`, `Delivery`, `Projector`, and `Handler` responsibilities. Keep the
   contract about OPDL behavior, not NATS APIs.
2. Change `platform/internal/events.Record` so every record contains:
   - event ID and event type;
   - schema version;
   - occurrence time;
   - project, environment, site, machine, and role identity;
   - causation ID and optional correlation ID;
   - immutable JSON payload.
3. Remove `Meta.Sequence`. Add `eventfabric.Receipt.Sequence` and
   `eventfabric.Delivery.Sequence` for the JetStream site sequence. Domain event
   payloads must not contain or depend on that transport sequence.
4. Add a route constructor in `platform/internal/eventfabric/route.go`. Produce
   `opdl.<site-scope>.event.<domain>.<fact>`, where `site-scope` is a stable
   lower-case, unpadded base32 encoding of a SHA-256 hash over length-prefixed
   project, environment, and site values. Parse event types only in
   `platform.<domain>.<fact>` form. Reject blank or extra tokens. Domain packages
   pass an event type, never a raw subject.
5. Name the site stream `OPDL_<UPPER_SITE_SCOPE>_EVENTS` and bind only
   `opdl.<site-scope>.event.>`. Use file storage and reject new
   events when limits are reached so required replay history is not silently
   removed.
6. Define at-least-once handling:
   - publish returns only after JetStream acknowledges durable acceptance;
   - a handler acknowledges only after its state change or resulting event is
     complete;
   - projection application is idempotent by event ID and domain identity;
   - malformed or unsupported events stop catch-up and make the node unready.
7. Define startup catch-up around one continuous ordered consumer. Start it,
   capture the journal high-water sequence, wait until the projector has applied
   that sequence, and keep the same consumer attached for live delivery. This
   avoids a replay-to-live subscription gap.
8. Replace the current registration storage algorithm and event catalog with
   this event flow:
   - the origin publishes `platform.registration.proposed` only after validating
     the HTTP input, trusted origin, and expected machine set;
   - every expected node handles the proposal and publishes exactly one
     `platform.registration.confirmed` or `platform.registration.rejected`
     decision;
   - the origin publishes accepted after every expected node confirmed;
   - the first proposed event for a unit key in site journal order claims the
     key; an identical proposal is a retry and a later different proposal is
     rejected as a conflict;
   - all query views are deterministic projections of these events.
9. Define `proposal_id` as a SHA-256 hash of versioned canonical request fields,
   trusted origin identity, and expected machines. Define decision IDs from
   proposal ID, decision kind, and deciding machine. Redelivery may try to
   publish the same outcome again, so reducers collapse decisions by this ID
   even after the NATS duplicate window expires.
10. Add table-driven tests in `eventfabric` for route and envelope validation.
    Add pure reducer tests in `registration` for proposal order, duplicate
    decisions, all-node acceptance, and conflict projection.
11. Update `docs/01-architecture.md`, `docs/02-registration.md`, and the relevant
    `doc.go` files with the accepted decisions before implementing the adapter.

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

## Open questions and recommendations

- Embedded or managed NATS cluster?
  Recommendation: embed one linked NATS server in each OPDL process for the POC.
  This preserves one deployable binary. Revisit when NATS needs an independent
  upgrade, security, backup, or failure boundary.
- Replica count by site size?
  Recommendation: enable JetStream on one deterministic node with one stream
  replica for one-node and two-node sites. Enable it on the first three nodes by
  sorted machine name with three replicas for larger sites. Non-storage nodes
  run Core NATS and route to the storage nodes. Do not form a two-member
  JetStream metadata group. Revisit when the descriptor can express dedicated
  storage roles or availability requirements change.
- All expected nodes or quorum acceptance?
  Recommendation: keep all expected nodes. This migration should change the
  coordination mechanism, not the registration business rule. Plan quorum as a
  separate domain change if offline acceptance is required.
- How much history is retained?
  Recommendation: retain all events by age and count until snapshots exist.
  Require a configurable byte limit, use `DiscardNew`, and make capacity visible
  in readiness. Revisit when measured replay time or disk use justifies snapshots.
- What does a valid POST return?
  Recommendation: return `202` and `proposal_id` only after durable publication.
  Report accepted, rejected, and conflict outcomes through status queries. This
  gives one deterministic asynchronous contract and removes the immediate `409`
  race.

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
- A two-node site has one JetStream storage node and no journal redundancy. It
  cannot publish or replay while that node is down. Make this explicit in
  deployment documentation.
