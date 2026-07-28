# Plan: Level hierarchy and scoped events

Status: draft for team review. Analysis date: 2026-07-28, based on branch
`mirmat-wip` at commit `1399a67` ("introducing Site -> Machine -> Instance
hierarchy").

## Goal

Finish the Site → Machine → Instance hierarchy so that events, storage, and
consumers each live at exactly one level, and use that structure to unblock
the registration use case (the service registry) — the one domain feature
currently dead in the tree. Keep future Master/Slave service activation in
sight without designing it now.

## Where the tree is today

The hierarchy exists as packages, but the responsibilities have not moved in
behind it:

| Fact | Evidence |
| --- | --- |
| Package levels exist: `internal/site/registration`, `internal/machine/redundancy`, `internal/instance/{events,state}` | tree under `platform/internal` |
| The abstract event contract (Event, Envelope, Factory, Publisher, Origin, Type, Severity, causation, BestEffort) sits at instance level although it declares no events and serves every level | `platform/internal/instance/events/doc.go` |
| The JSONL local record is the only working event surface; it is write-only and never read back by the platform | `platform/internal/instance/events/storage/jsonl`, `01-architecture.md` "Events" |
| Event distribution was removed; `hasEventStorage` is hardcoded `false`, so every instance takes the journal-less path | `platform/internal/app/runtime.go:114` |
| Registration is fully designed and partially implemented but blocked: `CommandService.Create`, `NewHandler`, and `app.open` return `ErrNotImplemented` | `registration/service.go:65`, `registration/handler.go:12`, `app/site.go:19` |
| Machine-level redundancy (lease file, ManageOwnership, epoch fencing in `instance/state`) works and is independent of distribution | `internal/machine/redundancy/doc.go` |
| Registration's conflict rule depends on one total order ("first proposed event in journal order claims the key") | `docs/02-registration.md` "Ordering and conflict" |

That last row is the pivot of the whole plan: any replacement distribution
either provides a total order per site, or the registration conflict rule
must be redesigned into a commutative merge. Step 06 decides this before any
distribution code is written.

## The level model this plan implements

- **Instance** — one process. Owns its local JSONL record. Instance events
  are operator evidence only; **no platform consumer reads them** (this is
  already the documented contract of the local record).
- **Machine** — one host, primary + optional standby. Machine events are
  shared between the two instances through a machine-wide store (a file
  first, later possibly SQLite). Only the Primary-Ownership holder writes;
  the passive instance only reads. Consumers of machine events are
  machine-level code.
- **Site** — all machines of a deployment. Site events are distributed to
  every machine (transport decided in step 06); each machine folds them into
  a local projection/cache. Consumers of site events are site-level code
  (registration handler and projection today).

Dependency rule: a higher level may import a lower level, never the reverse.

```text
internal/site     → may import internal/machine, internal/instance, internal/events
internal/machine  → may import internal/instance, internal/events
internal/instance → may import internal/events
internal/events   → imports none of the above (abstract contract)
internal/app      → composition root, may import everything
```

## Steps

One file per step. Each states goal, scope, concrete actions, acceptance
criteria, complexity, effort, dependencies, and open questions. Effort is in
focused person-days for one developer who knows this codebase; complexity is
Low / Medium / High (uncertainty and design risk, not just size).

| Step | Title | Complexity | Effort | Depends on |
| --- | --- | --- | --- | --- |
| [01](01-move-events-package.md) | Move the abstract event contract to `internal/events` | Low | 0.5–1 d | — |
| [02](02-enforce-dependency-rule.md) | Codify and enforce the level dependency rule | Low | 0.5–1 d | 01 |
| [03](03-event-scope-flag.md) | Add a scope (site/machine/instance) to the event contract | Low–Medium | 1–2 d | 01 |
| [04](04-storage-abstraction.md) | Per-level storage contracts (files now, SQL later) | Medium | 2–3 d | 03 |
| [05](05-machine-shared-store.md) | Machine-level shared event store (active writes, standby reads) | Medium–High | 3–5 d | 04 |
| [06](06-site-distribution-adr.md) | ADR: event journal vs full-state messaging for the site level | Medium | 2–3 d | 03 |
| [07](07-single-machine-registration.md) | File-backed site journal; registration works on a single-machine site | High | 5–8 d | 04, 06 |
| [08](08-multi-machine-distribution.md) | Multi-machine site distribution per the ADR | High | 8–12 d | 06, 07 |
| [09](09-service-activation-outlook.md) | Master/Slave service activation — constraints only | Low | 1–2 d | 06 |

Total: roughly **23–37 person-days**, with step 08 carrying most of the
uncertainty. Steps 01–03 are safe mechanical work that can start immediately;
step 06 is a decision gate that should run in parallel with 04–05 and be
reviewed by the team before 07 starts.

```text
01 ─► 02
 └──► 03 ─► 04 ─► 05
       └──► 06 ─────► 07 ─► 08
             └─────────────► 09
```

## Deliberate choices baked into the steps

- **Consumers live on the level of the events they consume.** Site consumers
  read site events, machine consumers read machine events, and instance
  events have no consumers at all. This keeps each store's read contract as
  small as its actual audience.
- **Files first, SQL later.** Every store is written behind a narrow Go
  interface so a JSONL implementation can be swapped for SQLite without
  touching producers or consumers (step 04). No SQLite is introduced in this
  plan.
- **No NATS for machine level.** The two instances share a host and already
  share a lease file; a shared file (or later a shared SQLite database) plus
  the existing lease/epoch mechanism is enough (step 05).
- **Registration first, activation later.** The plan restores the existing
  registration design; Master/Slave activation only contributes constraints
  (step 09) so the abstractions in 03/04 do not preclude it.
- **Scenarios are the acceptance harness.** Steps that change `platform/**`
  must re-run affected scenarios with `-count=1` (the scenario runner caches
  the built binary).

## Open questions for the team

Collected from the step files; these need owners before step 07:

1. Total order vs merge: keep the journal-order conflict rule, or redesign it
   commutatively so full-state messaging becomes possible? (step 06)
2. ~~Which events are machine-scoped vs instance-scoped — in particular
   redundancy facts stated by the *passive* instance, which the single-writer
   rule says may not write to the machine store.~~ **Answered in step 03,
   option (a):** an owner's ownership and activation transitions are
   machine-scoped, a passive instance's waiting and declining stay
   instance-scoped, and the passive instance therefore never has a
   machine-scoped fact to write. (steps 03, 05)
3. Who is the first real machine-level consumer? Building the shared store
   without one is speculative. (step 05)
4. Descriptor and builder impact of a machine-wide events path (new blueprint
   field, conformance tests, examples). (step 05)
