# Step 06 — ADR: event journal vs full-state messaging for the site level

| | |
| --- | --- |
| Complexity | Medium (analysis, no code) |
| Effort | 2–3 person-days |
| Depends on | 03 (scope vocabulary) |
| Blocks | 07 (partially), 08, 09 |

## Goal

One reviewed Architecture Decision Record choosing how site-scoped facts
reach every machine, evaluated **for the registration use case
specifically**, with service activation (Master/Slave) as a constraint, not
a driver. This is a decision gate: step 08 must not start before this ADR
is accepted, and step 07 starts only after its single-machine portion is
confirmed compatible with the decision.

## The crux to decide

Registration's correctness currently rests on one total order:

> The first proposed event for a key **in journal order** permanently
> claims it. Every node folds the same ordered journal, so every node
> derives the same winner. (`docs/02-registration.md`)

- An **event journal** (option A) provides that order and keeps the domain
  code as designed — at the cost of running ordered, retained,
  replicated storage (this is exactly what was removed in commit
  `ad931e7`, so the cost is not hypothetical).
- **Full-state messaging** (option B) has no site-wide order. Keeping it
  correct requires replacing "first in journal order" with a
  **commutative, deterministic merge rule** — e.g. lowest proposal-ID wins,
  or (origin timestamp, machine name) with explicit clock-skew tolerance.
  That is a domain-semantics change, not a transport swap, and it changes
  what a client observes under contention. The ADR must say so explicitly
  if B is chosen.

## Options to evaluate

| Option | Sketch | Prior art in repo |
| --- | --- | --- |
| A. Embedded ordered journal | Embedded NATS JetStream (or equivalent) per site; replay + live delivery; the removed Event Fabric design | `docs/01-architecture.md` Event Fabric section (marked TODO) |
| B. Full-state exchange | Each machine periodically/on-change broadcasts its whole registration state; receivers merge with a commutative rule (state-based CRDT style); anti-entropy loop | `docs/drafts/cmrdt.md` (notes), README idea list |
| C. Hybrid | Journal for registration now (keeps existing semantics); state exchange reserved for activation where whole-state is natural | — |
| D. Core NATS + app-level causal layer | Op-based CRDT over fire-and-forget transport with vector clocks and buffers in Go | `docs/drafts/cmrdt.md` — the draft itself concludes core NATS alone is insufficient |

Option D should be evaluated mainly to be formally rejected or accepted;
the draft's own analysis (no persistence, loss corrupts op-based CRDTs)
makes it a poor fit for a platform whose nodes may be down for long
periods (registration explicitly keeps proposals pending indefinitely for
an offline machine).

## Evaluation criteria (weigh in this order)

1. **Correctness under the acceptance contract** — offline expected
   machines keep proposals pending indefinitely; a node that missed weeks
   of history must converge to the same answers. Journals do this by
   replay; state exchange does it by merge; option D does not.
2. **Domain semantics preserved or consciously changed** — the total-order
   question above.
3. **Operational weight at realistic site sizes** — sites are 1–3 machines
   (storage-node selection in `01-architecture.md` caps replicas at 3);
   full-state payloads for a registry this small are trivially cheap, which
   is the strongest argument for B/C.
4. **Testability with files only** — can a single-machine site (step 07)
   and multi-machine tests run without external processes; can the step 04
   site contract be satisfied by a file implementation locally.
5. **Audit/replay value** — the journal doubles as site history; state
   exchange keeps only the latest state unless events are also retained
   locally.
6. **Fit for future activation** — activation state (who is Master) is
   naturally last-writer-wins whole-state; a registry claim is naturally
   first-writer-wins. The ADR should note per-use-case transports are
   acceptable (the README idea list already leans this way).

## Actions

1. Write the ADR (suggested home: `docs/adr/0001-site-distribution.md`,
   creating the convention) covering: context, the four options, the
   criteria table filled in, decision, and consequences — including the
   exact conflict rule if B/C changes it, and the migration impact on
   `docs/02-registration.md`.
2. Prototype nothing beyond a whiteboard-level merge-rule proof for option
   B: demonstrate on paper that two machines merging concurrent conflicting
   proposals converge and that the loser is reported with the existing
   `registration_key_conflict` reason.
3. Review with the team; record dissent in the ADR.
4. Amend the step 04 site-journal interface if the decision requires it
   (e.g. drop durable named consumers for B, keep them for A/C).

## Acceptance criteria

- ADR merged with an explicit decision and an explicit statement of what
  happens to the "first in journal order claims the key" rule.
- Steps 07/08/09 are updated (in place, in this plan directory) to name
  the chosen option instead of branching on it.

## Risks / open questions

- The team may split on A vs C. Default recommendation if review stalls:
  **C** — it unblocks registration with unchanged semantics and leaves
  activation free to use whole-state later, at the cost of eventually
  running two mechanisms. Recording a default prevents this gate from
  blocking steps 04–05, which proceed regardless.
