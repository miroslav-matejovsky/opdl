# Step 04 — Per-level storage contracts (files now, SQL later)

| | |
| --- | --- |
| Complexity | Medium |
| Effort | 2–3 person-days |
| Depends on | 03 |
| Blocks | 05, 07 |

## Goal

Each level's event storage is a narrow Go interface with a file-backed
implementation, so the later move to SQLite (or any SQL store) swaps an
implementation without touching producers, consumers, or domain code.

## Design

The three levels need three different contracts, because their audiences
differ. Do **not** build one generic store: the instance record is
write-only, the machine store has one writer and one local reader, and the
site journal needs ordered replay. A shared abstraction would be the union
of all three and would over-promise everywhere.

| Level | Contract | Backed by (now) | Later |
| --- | --- | --- | --- |
| Instance | existing `events/storage.Backend` (Store/Close) — unchanged | JSONL local record | stays JSONL; SQL never needed (no reader) |
| Machine | `machine` store: append + tail | shared JSONL file under a machine-wide path | shared SQLite (lease already serializes the writer) |
| Site | `site` journal: publish + ordered replay-then-follow | per-site file (step 07, single machine) | per the step 06 ADR |

Contracts live at the level of their consumers (consumers-on-same-level
rule), not in `internal/events`:

1. **Instance** — nothing to do. `storage.Backend` and the fan-out
   `Publisher` moved in step 01 stay as they are; the JSONL record remains
   the mandatory first backend of every publisher.

2. **Machine** — new package `internal/machine/eventstore` (name open)
   defining roughly:

   ```go
   type Appender interface {
       Append(ctx context.Context, envelope events.Envelope) error
       Close(ctx context.Context) error
   }

   type Reader interface {
       // Read replays retained envelopes in append order, then follows.
       Read(ctx context.Context, from Position) (<-chan Entry, error)
   }
   ```

   `Entry` = envelope + position. Position is the store's own ordinal
   (line/rowid), a delivery property, never part of the fact — same stance
   the envelope already takes on transport ordering. The scope safety net
   from step 03 lives in `Append`: a non-machine-scoped envelope is refused.

3. **Site** — define the interface only, shaped by what
   `internal/site/registration` already consumes (`events.Publisher` to
   publish; ordered `Delivery{Envelope, Sequence}` to fold; a durable,
   named-consumer read for the handler). Write it down as the minimal
   contract registration needs — publish, replay-then-live in one stream,
   at-least-once with sequence — and nothing NATS-shaped beyond that.
   Implementations come in steps 07/08; whether "durable consumer" survives
   the step 06 ADR is decided there.

4. Document, in each contract's doc.go, the write/read matrix:
   instance = write-only; machine = active instance writes, passive
   instance reads; site = active instance publishes, every machine reads.

## Actions

1. Add the machine store package with the interfaces above and a JSONL
   implementation reusing `utils/atomicfile` conventions and the existing
   jsonl backend's concurrency discipline (it already proves non-interleaved
   appends under concurrency in its tests).
2. Add the site journal interface package (`internal/site/eventfabric`) with
   no implementation yet.
3. Contract tests written against the interfaces (not the file
   implementation) so a future SQLite implementation runs the same suite:
   append/read round-trip, order preservation, reopen-appends semantics,
   scope refusal, follow-after-replay without gap or duplicate.
4. Update `docs/01-architecture.md` boundaries table.

## Acceptance criteria

- `go test ./...` green; the machine store's contract-test suite passes
  against the JSONL implementation.
- No level package imports another level's store implementation; only
  `internal/app` composes concrete stores (verified by the step 02 test).
- The site journal interface compiles against registration's existing needs
  (a stub implementation in tests can drive `Projection.Apply`).

## Implementation notes (done)

Landed as written, with these decisions recorded because later steps depend
on them:

- **Names.** The machine store is `internal/machine/eventstore` (the plan left
  the name open). The site contract is `internal/site/eventfabric`, not
  `journal`: Event Fabric is what `docs/01-architecture.md` and
  `registration/doc.go` already call the site's distribution, and the doc
  comment referring to `eventfabric.Delivery.Sequence` predates the package.
- **`Delivery` moved down to its consumers' level.** It was in
  `internal/events/storage`, which owns no level and now holds only `Backend`
  and the fan-out `Publisher`. Sequence is a site concept, so it is
  `eventfabric.Delivery` and `registration.Projection.Apply` folds that.
- **A read result is an entry *or* a failure.** Both `eventstore.Read` and
  `eventfabric.Consumer.Follow` return `<-chan Result`, where `Result` carries
  either the value or the error that ended the stream, rather than the plan's
  `<-chan Entry`. A consumer that stops receiving otherwise cannot tell a
  cancelled follow from a store it has lost, and those call for opposite
  reactions.
- **`Ack` is on the site contract.** At-least-once with a durable named
  consumer is meaningless without a point at which progress is recorded, so
  `Follow` and `Ack` are the two methods and there is nothing else.
- **No `utils/atomicfile`.** The plan suggested reusing it; the machine store
  is append-only and fsynced per line, so there is no rewrite to make atomic.
  What it does reuse is the JSONL backend's concurrency discipline: one mutex,
  one `Write` per envelope, sync before returning.
- **The contract suite is in `eventstore`'s external test package**, driven by
  an `implementations` map, rather than in an exported `eventstoretest`
  package. It is written against `Appender`/`Reader` and never against a file,
  so a SQLite implementation adds one map entry; promote it to its own package
  when there is a second implementation to share it with, not before.
- **A partial trailing line is held, not decoded.** The follower keeps what it
  has read of a line without a newline until the rest arrives, so a reader
  never sees a truncated envelope from a writer that is mid-append.
- **Follow is polling**, at a 100ms interval that is an unexported constant of
  the file implementation and named in its doc as not part of the contract.

## Risks / open questions

- Interface prematurity on the site side: until step 06 decides the
  distribution model, the site contract is a best guess. Mitigation: it is
  interface-only here and deliberately minimal; the ADR may amend it before
  any implementation exists.
- Follow semantics on Windows shared files (change notification vs polling)
  can hide in the `Reader`; keep polling as the first implementation and
  note the interval as an operational constant, not a contract.
