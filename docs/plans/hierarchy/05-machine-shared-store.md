# Step 05 — Machine-level shared event store

| | |
| --- | --- |
| Complexity | Medium–High |
| Effort | 3–5 person-days |
| Depends on | 04 |
| Blocks | — (08 benefits but does not require it) |

## Goal

Machine-scoped events are written once per machine to a shared,
machine-wide store that both instances can read, instead of landing only in
the writing process's private JSONL. Only the Primary-Ownership holder
writes; the passive instance reads.

## Why

Today an ownership transition is recorded in whichever instance's local
record happened to state it, so no single place holds the machine's
history. A machine store gives the passive instance and deployment tooling
one ordered account of the machine, and it is the pattern the site level
will reuse (single writer guarded by a lease, ordered append, local read).

## Design

1. **Location.** A machine-wide path authored in the blueprint and carried
   in the descriptor, exactly like the lease file: e.g. a `machine_events`
   file beside `lease` in the descriptor's machine part. This touches the
   whole descriptor pipeline — `builder/deployment`, blueprint HCL parsing,
   `platform/config`, `examples/*/project.hcl`, and `conformance-tests`
   (which keep builder and platform descriptors in step). Do not bury this:
   it is a third of the step's effort. A machine without a standby still
   gets the file (single-instance machines have machine facts too), unlike
   the lease, which exists only with a standby.

2. **Single writer, enforced twice.**
   - *Sequencing:* `redundancy.ManageOwnership` already guarantees Passive
     has returned before Active runs, and the lease excludes the other
     process. The machine-store `Appender` is opened inside the active
     composition and closed before ownership is released, the same
     bracketing the site fabric had.
   - *Fencing:* every appended envelope is stamped with the writer's
     current epoch (`internal/instance/state` — the counter that advances
     on start and on activation and is documented as a fencing token). A
     reader ignores nothing, but tooling can detect an append from a stale
     epoch, and a later SQLite implementation can turn the epoch into a
     hard write guard. Decide during implementation whether the epoch rides
     in the envelope (new field) or in the store's `Entry` beside the
     position — recommended: in `Entry`, because it is a property of the
     write, not of the fact.

3. **Read path.** The passive instance tails the store through the step 04
   `Reader`. First consumer must be named before this step starts (open
   question 3 in the README); candidates:
   - the passive instance's view of the machine's ownership history for its
     own API surface (`GET /instance` could answer "who was active, since
     when" without asking the peer);
   - deployment tooling reading one file per machine instead of two.
   If the team cannot name a consumer it values, **defer this step** after
   landing only the descriptor field — the store without a reader is
   speculative.

4. **Event flow change.** Machine-scoped events (per the step 03
   classification: owner-stated redundancy facts) are published through a
   publisher whose backends are the local JSONL record **and** the machine
   store — the existing fan-out `Publisher` composes this with no new
   machinery. Passive-stated facts remain instance-scoped and keep the
   JSONL-only publisher.

5. **Crash semantics.** Append-only JSONL, fsync policy identical to the
   local record. A torn final line after a crash is tolerated by the reader
   (skip a trailing partial line, surface it to diagnostics) — a machine
   store that halts its reader on the very artifact of the failover it
   should describe would be self-defeating.

## Actions

1. Blueprint + descriptor + config + conformance work for the machine-wide
   path (see Design 1).
2. Wire the machine store's open/close into the active composition in
   `internal/app` (`runActive`), bracketed inside ownership exactly like
   the site once was.
3. Route machine-scoped events to the two-backend publisher; keep
   instance-scoped events on the JSONL-only one.
4. Implement the first named consumer (Design 3).
5. Scenario: extend the warm-standby scenario to assert that after a
   forced-kill failover the machine store holds both the old owner's last
   facts and the new owner's `ownership_acquired`, in order, readable by
   either instance. Re-run with `-count=1`.

## Acceptance criteria

- One machine, both instances: every machine-scoped event appears exactly
  once in the machine store with the writer's epoch; the passive instance's
  reader observes appends from the active one.
- Kill-based failover leaves the store readable; the new owner appends
  after the old owner's last entry; no interleaving.
- Machines without a standby work unchanged (single instance is always the
  writer).
- Conformance tests pass with the new descriptor field on both builder and
  platform sides.

## Risks / open questions

- **YAGNI risk** — see Design 3; do not build the reader without a consumer.
- Windows file-share semantics: one writer with append + one reader with
  share-read is the normal case, but verify the exact open flags in the
  jsonl backend allow a concurrent reader at all (the local record never
  needed one).
- Whether `platform.app.failover_readiness_changed` is machine- or
  instance-scoped is genuinely arguable (stated by the passive instance,
  consumed by machine tooling); the single-writer rule forces
  instance-scoped for now — revisit if the machine store later grows a
  multi-writer design (e.g. SQLite with per-writer fencing).
