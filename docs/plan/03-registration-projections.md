# Stage 3: Registration projections

## Outcome

Replace registration's five shared collections and periodic reconciliation with
events, durable handlers, and node-local projections.

Complexity: High.

Estimated time: 5-8 engineering days.

Depends on: [Stage 2](02-event-fabric.md).

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

1. Replace storage records with versioned event payloads. A proposal carries its
   deterministic proposal ID, complete request fields, trusted origin identity,
   and the expected machine set used for its decision.
2. Implement pure, ordered, idempotent reducers for:
   - proposal status by proposal ID;
   - selected proposal by unit key;
   - confirmations and rejections by machine;
   - accepted registrations;
   - resolved conflicts.
3. Resolve concurrent proposals by their sequence in the site journal. The
   first valid proposal for a unit key wins. An identical proposal is an
   idempotent retry. A later different proposal becomes a rejected contender.
4. Replace each reconciler scan with a durable handler scoped to the node:
   - validate new proposals and publish one node decision;
   - on the origin node, observe all required confirmations and publish the
     final accepted event;
   - publish conflict rejection for a losing proposal.
5. Make decision identities deterministic from proposal ID, decision kind, and
   deciding machine. A handler may safely repeat publication after redelivery.
6. Acknowledge an input event only after any required resulting event is
   durably accepted. If publication is uncertain, leave the input unacknowledged
   and retry.
7. Change the service API to publish commands and read only local projections.
   It must not query NATS synchronously for state.
8. Simplify the HTTP contract for asynchronous processing:
   - invalid input returns `400` without publishing;
   - a durably published proposal returns `202` and its proposal ID;
   - an unavailable journal returns `503`;
   - status and list endpoints read the local projection and may expose its
     current journal sequence;
   - conflict is a projected outcome, not a race-sensitive immediate POST
     result.
9. Open the API only after registration projections have completed startup
   replay. Continue applying live events while serving queries.
10. Replace fabric-heavy registration tests with pure reducer tests and focused
    NATS integration tests.

## Observable registration flow

1. Node A validates an HTTP request and publishes a proposal.
2. The site journal assigns the proposal's order and acknowledges it.
3. Every expected node's durable handler processes the proposal, including a
   node that reconnects later.
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

## Open questions

- Should expected machines be copied into each proposal event or derived from
  the local descriptor during projection? Copying makes historical decisions
  self-contained and is the recommended POC choice.
- Does every node run registration validation, or only nodes that host the
  registration service? The expected decision set must be explicit.
- Should rejected proposals remain visible forever, or follow the journal's
  retention policy?
- Should status lookup use the new proposal ID, the unit key, or both?
- Is the origin solely responsible for final acceptance, or may any node emit
  the same deterministic acceptance event? Origin-only is simpler and matches
  current behavior, but acceptance waits for origin recovery.

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

