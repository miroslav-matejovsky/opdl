# Step 09 — Master/Slave service activation: constraints only

| | |
| --- | --- |
| Complexity | Low (design note, no implementation) |
| Effort | 1–2 person-days |
| Depends on | 06 |
| Blocks | — |

## Goal

A short design note (suggested home: `docs/drafts/service-activation.md`,
promoted out of drafts when work starts) capturing what service activation
will need from the hierarchy, so nothing in steps 03–08 has to be undone
when activation is implemented. **No activation code is written in this
plan.**

## What activation is, as currently understood

Registered service units elect one **Master** per unit (or unit group) with
the others as Slaves. This is a *service-level* role over the registry —
explicitly not the platform's per-machine primary/standby, which stays
owned by `internal/machine/redundancy` and independent of it.

## Constraints to verify against steps 03–08

1. **Scope fits.** Activation facts ("unit X's master is machine M") are
   site-scoped; the step 03 scope vocabulary needs no fourth level.
2. **Per-use-case transport is allowed.** The README idea list and the step
   06 criteria both anticipate that activation may prefer whole-state
   messaging (current-master is naturally last-writer-wins) even if
   registration uses a journal. Verify the step 04 site contract does not
   force one mechanism on every site-level consumer — i.e. a site-level
   consumer binds to a contract, and two contracts may coexist behind
   `internal/app` composition.
3. **Registry is the substrate.** Activation decisions read the
   registration projection (who is registered, where). The projection's
   query surface (`QueryService`) must stay consumable by another
   site-level package without reaching into registration internals — check
   whether a read-interface extraction is needed and note it, don't do it.
4. **Liveness input.** Master election needs a liveness signal
   (heartbeats/leases per service unit), which registration deliberately
   lacks today ("leases, heartbeats, and quorum are outside the current
   implementation"). Name it as the first genuinely new mechanism
   activation introduces, and check the machine store (step 05) is not
   accidentally assumed as its transport — liveness is site-scoped.
5. **Failover interplay.** When a machine's platform fails over
   primary→standby, its hosted service units keep their Master/Slave roles
   (machine identity, not process identity, holds the role — same stance
   registration takes on voting). Write this down; it falls out of the
   existing origin/identity design but only if nothing in 05/08 keys
   site-visible state to a process.

## Actions

1. Write the note with the five constraint checks above, each answered
   against the landed (or planned) state of steps 03–08, with concrete
   package references.
2. File deviations found as amendments to the affected step file or as
   backlog entries — this step produces no code changes anywhere.
3. Capture open product requirements for activation (election policy,
   manual override, observability) as questions, not designs.

## Acceptance criteria

- The note exists, each constraint has a verdict ("holds because…" /
  "needs amendment X in step Y"), and the team has reviewed it.
- No step 03–08 abstraction is left that would have to be broken (rather
  than extended) to add activation.

## Risks / open questions

- Activation semantics are the least-specified part of the whole effort;
  the note must resist designing them. Its only job is keeping the door
  open.
