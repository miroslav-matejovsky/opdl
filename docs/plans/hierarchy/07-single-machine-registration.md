# Step 07 — File-backed site journal; registration works on a single-machine site

| | |
| --- | --- |
| Complexity | High |
| Effort | 5–8 person-days |
| Depends on | 04 (site contract), 06 (compatibility check only) |
| Blocks | 08 |

## Goal

Registration runs end-to-end on a single-machine site with a file-backed
site journal: `POST /registrations` returns `202`, the handler confirms and
accepts, queries answer from the projection, and state survives restart by
replay. This is the first user-visible payoff of the plan and it de-risks
step 08 by proving the whole vertical slice below the transport.

## Why this slice first

- A single machine trivially has a total order (one writer, one file), so
  this step is valid under **any** outcome of the step 06 ADR — the only
  check needed is that the file journal's semantics are a subset of the
  chosen distribution's. Confirm that before starting; do not wait for 08.
- Everything blocked today unblocks here: `hasEventStorage`
  (`runtime.go:114`), `app.open` (`site.go:19`),
  `registration.CommandService.Create` (`service.go:65`),
  `registration.NewHandler` (`handler.go:12`).
- The existing scenario floor is single-machine (`01-architecture.md` notes
  the multi-machine suite was removed), so this step is fully provable with
  today's harness.

## Design

1. **Journal implementation.** Implement the step 04 site contract with a
   per-site JSONL file under the machine's data root: ordered append (the
   line number is the sequence), replay-then-follow on one stream,
   publish = append. Reuse the machine-store implementation from step 05
   where possible — same single-writer bracket (opened in the active
   composition, closed before ownership release), same lease + epoch
   guarding, same torn-tail tolerance. On a single-machine site the "site
   journal" and the "machine store" differ only in scope and contract, not
   in mechanics.
2. **Descriptor.** The journal file's path and the site's event-storage
   presence come back into the descriptor (the removed `event storage`
   record), restoring `hasEventStorage` as a descriptor fact instead of a
   hardcoded false. Builder + config + conformance impact as in step 05 —
   plan them together if the steps land close in time.
3. **Command path.** Implement `CommandService.Create`: domain validation
   (blank advertised name, unknown role → the documented `400` reasons),
   proposal-ID derivation (already implemented in `identifiers.go`),
   publish `Proposed` through the site publisher, return the receipt. The
   `503 journal_unavailable` path maps from publish failure.
4. **Handler.** Implement `NewHandler` per `doc.go`'s contract: consume
   proposals/confirmations, wait for its own projection to have applied the
   input, publish exactly one deterministic consequence (`Confirmed`,
   `Rejected`, `Accepted`). On a single-machine site the expected set is
   `[self]`, so acceptance follows the machine's own confirmation — the
   full flow still exercises every event type.
5. **Composition.** Implement `app.open`: open journal → projector catch-up
   to captured high-water → attach handler → catch up again → readiness →
   swap the active HTTP surface in. Follow the documented bootstrap order
   in `01-architecture.md`; the failover monitor and lag bound come back to
   life against the real `progressFabric`.
6. **Passive side.** `runPassive` opens the read-only journal follower and
   reports failover readiness again, replacing the current
   no-event-storage shortcut.

## Actions

1. Compatibility check against the accepted ADR (half a day, gate).
2. Journal implementation + contract tests (step 04 suite).
3. Descriptor/builder/config/conformance changes.
4. `Create`, `NewHandler`, `app.open`, passive follower — in that order,
   each behind passing unit tests (`Projection` reducers already have
   tests; the handler's determinism tests come with it).
5. Update `docs/02-registration.md` and `docs/01-architecture.md`: remove
   the "does not currently run" TODO banners for the single-machine case,
   state the multi-machine limitation instead.
6. Scenarios: restore/extend a registration scenario (propose → poll →
   accepted; restart → same answers by replay; standby failover preserves
   registrations). Run with `-count=1`.

## Acceptance criteria

- On a standby-enabled single-machine build: POST returns `202`; polling
  reaches `accepted`; `GET /registrations` and `/conflicts` answer per the
  API contract; a second identical POST is idempotent (same proposal ID); a
  conflicting POST is projected `rejected` with `registration_key_conflict`.
- Kill the active instance: the standby takes ownership, replays, and
  serves the same registration answers.
- Full restart of the machine: answers reconstructed from the journal file
  alone.
- No `ErrNotImplemented` remains on the registration path; `hasEventStorage`
  reads the descriptor.

## Risks / open questions

- Largest step; if it slips, split the passive follower + failover
  readiness (Design 6) into its own follow-up — the active-only slice
  already delivers the API.
- The durable-handler abstraction ("worked through what the journal
  retained for them") is easy to over-build for one file; implement the
  minimum the handler contract needs (a persisted consumer position beside
  the journal) and resist generalizing until 08.
