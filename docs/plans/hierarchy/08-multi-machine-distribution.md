# Step 08 — Multi-machine site distribution per the ADR

| | |
| --- | --- |
| Complexity | High (largest uncertainty in the plan) |
| Effort | 8–12 person-days (re-estimate after step 06) |
| Depends on | 06 (decision), 07 (working single-machine slice) |
| Blocks | — |

## Goal

Site-scoped events reach every machine of a multi-machine site, each
machine folds them into the same registration answers, and a machine that
was offline converges after it returns. The concrete design is the step 06
ADR's; this file fixes the scope, the invariants any option must meet, and
the work items common to all options. **Rewrite this file to the chosen
option when the ADR is accepted** — the branching below is planning
material, not something to implement twice.

## Invariants (option-independent acceptance bar)

1. Every machine derives the same winner for a contested key — either from
   one order (A/C) or from a commutative merge (B) — with no
   reconciliation pass and no window of disagreement that survives
   convergence.
2. An expected machine that is offline keeps a proposal pending
   indefinitely and the projection names it; the machine converges after
   any downtime, bounded only by transfer time.
3. A client may poll any machine and get the same answer once both are
   converged (the documented contract).
4. Local redundancy stays fully independent: failover on one machine never
   depends on the site transport being up (the machine store and lease are
   local).
5. Idempotency lives in the domain identities (proposal ID, decision ID),
   never in transport deduplication — already the documented stance;
   at-least-once delivery is acceptable everywhere.

## Work items common to every option

- **Topology into the descriptor.** The builder already resolves site-wide
  machine lists ("Site-wide lists are resolved by the builder, never
  authored"); the expected-machine set feeding
  `registration.Open`/`topology()` (`app/site.go:26` currently returns
  `[self]`) must come from the resolved descriptor. Builder, config,
  conformance, examples.
- **Local state cache.** Each machine folds site events/state into its
  local projection at startup and on delivery. Memory-only + full replay
  is the current documented stance; introducing SQLite as a projection
  cache is an *optional* extension here, behind the step 04 abstraction,
  only if replay cost demands it — measure first.
- **Readiness.** The bootstrap readiness sequence (catch-up before serving)
  extends to "caught up with the site", per the documented order.
- **Scenarios.** Multi-machine scenarios return: two-machine acceptance
  (the `02-registration.md` end-to-end example with late node B),
  contention from two origins, machine-down-and-return convergence,
  failover on one machine during site traffic. These are the expensive
  deliverable of this step; budget ~a third of the effort.

## Option-specific sketches (to be replaced by the ADR outcome)

- **A / C (ordered journal, e.g. embedded NATS JetStream):** restore the
  Event Fabric adapter behind the step 04 site contract; storage-node
  selection and replica policy per the existing architecture doc; the open
  product question in `docs/backlog` about the leader-election write-reject
  window returns with it.
- **B (full-state exchange):** define the state document (registration
  projection snapshot + version vector or per-origin monotonic counters),
  the merge function (the ADR's conflict rule), the exchange trigger
  (on-change + periodic anti-entropy), and a minimal transport (core NATS
  or plain HTTP between platform APIs — note today's APIs are
  loopback-only by design, so a site transport is a **new, platform-owned,
  authenticated network surface** whichever option wins; A hides it inside
  NATS cluster ports, B must own it explicitly).
- Either way, site events retained locally (machine store or journal file)
  keep the audit/replay property the platform documents.

## Acceptance criteria

- The invariants above demonstrated by the restored multi-machine
  scenarios, run with `-count=1`.
- `docs/01-architecture.md` Event Fabric TODO banner and
  `docs/02-registration.md` TODO banner removed; docs describe the shipped
  mechanism.
- Single-machine sites from step 07 run unchanged (the floor remains).

## Risks / open questions

- Effort here is a placeholder until the ADR lands; option B likely sits at
  the low end for transport but pays it back in merge-rule testing, option
  A the reverse.
- New network surface between machines needs at least a minimal
  authentication story (the architecture doc already flags that any
  non-loopback API "must be platform-owned, authenticated, and
  authorized") — scope the minimum for a trusted-LAN deployment and record
  the rest in the backlog, or this step doubles.
