# Step 06: decide site distribution and its event-backend contract

| | |
| --- | --- |
| Complexity | Medium |
| Effort | 2-3 person-days |
| Depends on | Implemented hierarchy baseline |
| Blocks | 07, 08, 09 |

## Goal

Record one reviewed decision for how site-scoped envelopes receive a durable
site order and reach every machine. The decision must fit the implemented
scope-based publisher model and registration's current ordering rule.

## Current constraints

- `events/storage.Publisher` stamps once and fans out synchronously.
- `instance/eventlog.Backend` stores every scope.
- `machine/eventstore.Backend` stores only machine scope.
- `eventfabric.Consumer` exposes ordered replay-then-follow and durable
  acknowledgement.
- Site publication uses `events.Publisher`; there is no separate site publisher
  interface.
- Registration selects the first proposal for a key by
  `eventfabric.Delivery.Sequence`.
- Local ownership must continue when site distribution is unavailable.

The ADR must define how a site adapter joins this model. The expected shape is a
scope-filtering `storage.Backend` for publication plus an
`eventfabric.Consumer` for delivery. If the selected design needs a different
shape, amend the contracts before implementing it.

## Decision to make

### Ordered journal

Preserve the registration model. A site adapter accepts site-scoped envelopes,
assigns one durable order, and supports replay, live follow, and durable
acknowledgement. Evaluate an embedded production transport and a lightweight
local implementation for tests and single-machine sites.

### Convergent state exchange

Remove the site-order assumption. This requires changing registration before
transport implementation:

- replace "first journal proposal wins" with a deterministic commutative rule;
- remove sequence from conflict correctness;
- redefine `eventfabric.Consumer`; and
- update registration docs, projection tests, and public expectations.

Core fire-and-forget messaging alone is not sufficient. A machine offline for an
arbitrary duration must recover missing state.

## Required decisions

The ADR must state:

1. The selected convergence model and exact registration conflict rule.
2. The site adapter's write and read contracts.
3. Where durable event/state data lives on a single-machine site.
4. How multi-machine data survives an offline machine.
5. Whether durable consumer positions are machine-scoped and shared by primary
   and standby.
6. How site configuration is authored in the blueprint and resolved into each
   machine descriptor.
7. How the runtime remains locally healthy when site distribution is
   unavailable while refusing stale domain operations.
8. Which network endpoints are required and how they are authenticated.

## Actions

1. Write `docs/adr/0001-site-distribution.md`.
2. Compare ordered journal and convergent exchange against the requirements
   above.
3. Prove the selected conflict rule with concurrent proposals from two machines.
4. Amend `eventfabric` interfaces and their contract tests if required.
5. Rewrite steps 07 and 08 to name the selected implementation.
6. Update [Events](../../02-events.md) with the accepted decision.

## Acceptance criteria

- The ADR has one explicit decision, not several implementation branches.
- Registration convergence is specified for concurrent conflicts and long
  machine outages.
- The publisher/backend lifecycle is defined without weakening local ownership.
- Steps 07 and 08 contain no unresolved transport alternative.
