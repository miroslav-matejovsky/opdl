# Stage 3: Registration projections

## Outcome

Replace registration's five shared collections and periodic reconciliation with
events, durable handlers, and node-local projections.

Complexity: High.

Estimated time: 5-8 engineering days.

Depends on: [Stage 2](02-event-fabric.md).

Implementation status: Complete. The event-backed projection, command and query
services, and durable handler are active in `internal/registration`. Stage 4
completed the atomic runtime and HTTP cutover. See the
[migration findings catalog](findings/README.md).

## State mapping

| Current distributed state | Replacement |
| --- | --- |
| `registration-contenders` | Ordered `registration.proposed` events |
| `registration-confirmations` | Per-node confirmed or rejected events |
| `registration-acceptances` | `registration.accepted` events |
| `registration-requests` | Local registration status projection |
| `registrations` | Local accepted-registration projection |
| Periodic enumeration and repair | Durable event handlers and deterministic replay |

## Work

1. Replace `records.go` storage records with versioned payloads in `events.go`.
   Add `Proposed`, keep or revise `Confirmed`, `Rejected`, and `Accepted`, and
   remove payload meanings tied to collection writes. A proposal carries its
   deterministic proposal ID, complete request fields, trusted origin identity,
   ordered expected machine names. Occurrence time stays in the shared envelope
   and is not part of the proposal ID.
2. Add `projection.go` with a `Projection` type that owns ordinary Go maps under
   a read/write mutex. Its `Apply(eventfabric.Delivery)` method dispatches to
   pure, ordered, idempotent reducers for:
   - proposal status by proposal ID;
   - selected proposal by unit key;
   - confirmations and rejections by machine;
   - accepted registrations;
   - resolved conflicts.
   Query methods return copies and never expose the maps. Unit tests call the
   reducers directly without NATS.
3. Resolve concurrent proposals by `Delivery.Sequence` in the site journal. The
   first proposed event for a unit key permanently claims it. An identical
   proposal is an idempotent retry. A later different proposal becomes a
   rejected contender. If an expected node rejects the selected proposal, its
   status is rejected but the key is not automatically released. Key release is
   a separate future domain event, not implicit cleanup.
4. Add `handler.go` and replace each reconciler scan with one durable handler
   scoped to the node:
   - validate new proposals and publish one node decision;
   - on the origin node, observe all required confirmations and publish the
     final accepted event;
   - publish conflict rejection for a losing proposal.
   Register an explicit route list. Do not subscribe to `>` or dispatch unknown
   registration events dynamically.
5. Before a handler decides from an input delivery, call
   `Projection.WaitApplied(ctx, delivery.Sequence)`. This ensures the local view
   includes the event and every earlier journal event. The method returns when
   `Apply` advances the projection's recorded sequence and fails on cancellation
   or projection error.
6. Make decision identities deterministic from proposal ID, decision kind, and
   deciding machine. A handler may safely repeat publication after redelivery.
7. Acknowledge an input event only after any required resulting event is
   durably accepted. If publication is uncertain, leave the input unacknowledged
   and retry.
8. Rewrite `registration.Open` to accept a narrow `eventfabric.Publisher`, the
   local `Projection`, trusted `Location`, and expected machines. Return a
   command service and query service. Delete `store.go` and `reconciler.go` only
   after their tests have direct event-based replacements.
9. Change the command service to validate and publish only. Change the query
   service to read only local projections. Neither service may query NATS
   synchronously for state.
10. Simplify the HTTP contract for asynchronous processing:
   - invalid input returns `400` without publishing;
   - a durably published proposal returns `202` and its proposal ID;
   - an unavailable journal returns `503`;
   - status and list endpoints read the local projection and may expose its
     current journal sequence;
   - conflict is a projected outcome, not a race-sensitive immediate POST
     result.
11. Update `platform/api`, `internal/httpapi`, the OpenAPI source, generated .NET
    SDK, and their tests around `proposal_id` and asynchronous conflict status.
12. Open the API only after registration projections have completed startup
   replay. Continue applying live events while serving queries.
13. Replace `site_test.go`, `contenders_test.go`, and
    `contenders_olric_test.go` coverage with pure reducer and handler tests.
    Keep only cross-process delivery behavior in NATS integration tests and
    black-box scenarios.

## Handler consequences

| Input event | Handler action |
| --- | --- |
| `registration.proposed` | Every expected node waits for its projection, then publishes one confirmed decision for the selected proposal or one rejected decision for a conflicting or invalid proposal. |
| `registration.confirmed` | The origin waits for its projection, then publishes accepted when all expected confirmations are present. Other nodes acknowledge without publishing. |
| `registration.rejected` | No handler route. The node-wide projector records it. |
| `registration.accepted` | No handler route. The node-wide projector records it. |

Every publication uses a stable decision ID. No handler consumes its own output
unless the table requires another finite transition.

## Observable registration flow

1. Node A validates an HTTP request and publishes a proposal.
2. The site journal assigns the proposal's order and acknowledges it.
3. Every expected node's durable handler processes the proposal in journal
   order, including a node that reconnects later.
4. Each node publishes one confirmation or rejection.
5. Node A observes every required confirmation and publishes acceptance.
6. Every node applies the same events to its local projection.
7. Each node answers registration queries from its own projection.

If an expected node remains offline, the proposal remains pending. This retains
the current all-node acceptance rule without shared state.

## Tests

- Full replay from an empty projection produces the same views on every node.
- A node that starts late handles the retained proposal and catches up.
- Duplicate proposal, decision, and acceptance deliveries are harmless.
- Two conflicting proposals converge to the journal-order winner.
- A crash between receiving a proposal and publishing a confirmation causes a
  retry and one projected confirmation.
- A crash after publishing but before acknowledging does not create a second
  projected decision.
- Unknown event versions stop projection catch-up and readiness.
- HTTP queries never call the Event Fabric or NATS.

## Exit criteria

- Registration has no dependency on `fabric.Fabric` or `fabric.Collection`.
- Registration has no periodic reconciler or full-site scan.
- All registration query behavior comes from a local projection built only from
  events.
- Multi-node tests pass when one node starts late or restarts.
- No registration correctness rule depends on clock comparison or Olric
  membership stability.

## Open questions and recommendations

- Capture expected machines or derive them during replay?
  Recommendation: copy an ordered expected-machine list into `Proposed`. A
  historical decision must not change because a later binary has a different
  descriptor. Reject a proposal when its set does not match the origin's current
  trusted descriptor at publication time.
- Which nodes validate a proposal?
  Recommendation: use every expected platform machine for the first migration,
  matching current behavior. If only service-hosting machines should decide,
  change the builder to produce that explicit set before Stage 3.
- How long are rejected proposals visible?
  Recommendation: keep them for the same duration as the journal and rebuild
  them in the conflicts projection. Do not add separate deletion events or
  retention rules during migration.
- How is status addressed?
  Recommendation: make proposal ID the canonical status key. Keep unit-key list
  and conflict queries for current domain views. Do not overload one unit-key
  status route when several proposals may exist.
- Who emits final acceptance?
  Recommendation: only the trusted origin handler. This matches current
  ownership and prevents several nodes from racing to state the same fact. The
  proposal remains pending while the origin is offline and completes when its
  durable handler resumes.

## Risks

- Event handlers that publish events can form loops. Each handler must list the
  exact event types it consumes and consequences it may emit.
- Replaying a side-effecting handler could reissue decisions. Projection replay
  and durable coordination handlers must remain separate mechanisms.
- An asynchronous POST changes when a client learns about a conflict. API and
  SDK contracts must state this clearly.
- A slow projector can serve stale data after startup. Expose lag and remove the
  node from readiness when lag exceeds an agreed bound.
- Changing the expected machine set after a proposal was published can change
  its outcome unless the set is captured in the event.
