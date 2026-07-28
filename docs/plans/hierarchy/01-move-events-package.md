# Step 01 — Move the abstract event contract to `internal/events`

| | |
| --- | --- |
| Complexity | Low |
| Effort | 0.5–1 person-day |
| Depends on | — |
| Blocks | 02, 03 |

## Goal

The abstract event contract lives at `platform/internal/events`, importable
by every level, and the instance level keeps only what is genuinely
instance-scoped: the JSONL local-record backend.

## Why

`internal/instance/events` owns the one event contract for the whole
platform — its own doc.go says "It owns no events of its own" — yet its path
claims it is instance-level. Site code (`internal/site/registration`) and
machine code (`internal/machine/redundancy`) already import it, which under
the dependency rule (step 02) would read as site importing instance
internals. Moving it makes the rule enforceable: `internal/events` is the
shared vocabulary, and level packages never import each other's internals to
publish a fact.

## Scope

**In:** package moves, import rewrites, doc updates.
**Out:** any behavior change; any new field on Envelope (that is step 03);
renaming types.

## Actions

1. Move `platform/internal/instance/events` → `platform/internal/events`.
   Contract files: `event.go`, `envelope.go`, `factory.go`, `publisher.go`,
   `origin.go`, `type.go`, `id.go`, `severity.go`, `causation.go`,
   `besteffort.go`, `doc.go`, plus their tests. Package name stays `events`.
2. Move `platform/internal/instance/events/storage` (the abstract `Backend`
   interface, `Delivery`, and the fan-out `Publisher`) →
   `platform/internal/events/storage`. It is transport- and file-agnostic,
   so it belongs beside the contract, not under instance.
3. Keep the JSONL backend at instance level. Recommended new home:
   `platform/internal/instance/record` (package `record`), matching the
   existing vocabulary "the local record" used throughout doc.go and
   `01-architecture.md`. Alternative: `internal/instance/events/jsonl`
   — rejected here because after the move that directory would hold nothing
   but jsonl and the path would again suggest instance owns the contract.
4. Rewrite imports in `internal/site/registration`,
   `internal/machine/redundancy`, `internal/app`, `internal/httpapi` (test
   only), and anywhere else `go build ./...` finds.
5. Update the boundary table in `docs/01-architecture.md` (rows for
   `internal/instance/events` and its storage subpackages) and
   `platform/README.md`.
6. Delete the stray test litter under
   `internal/instance/events/storage/jsonl/.tmp/` if it is not already
   git-ignored, so the move does not carry it along.

## Acceptance criteria

- `go build ./...` and `go test ./...` pass in the `platform` module with no
  behavior change (pure move: no diff besides package paths and doc text).
- `internal/instance` contains no abstract event code; `internal/events`
  contains no file or transport code except the abstract fan-out.
- `task deadcode` still passes (the moved packages remain reachable from the
  cmd module or the scenarios test graph).
- Affected scenarios re-run green with `-count=1` (the runner caches the
  platform binary; a cached run proves nothing after a `platform/**` change).

## Risks / open questions

- ~~Naming of the JSONL home (`record` vs `eventlog` vs keeping `jsonl`) is a
  team taste call; the step is identical either way. Decide in review of
  this plan, not during the move.~~ **Decided: `platform/internal/instance/eventlog`**
  (package `eventlog`, files `eventlog.go` / `eventlog_test.go`). The error
  strings keep their `jsonl:` prefix: they name the on-disk format, which the
  move did not change, and `internal/app` asserts on that prefix to prove a
  boot failure identifies the local-record backend.
