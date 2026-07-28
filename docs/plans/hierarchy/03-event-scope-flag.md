# Step 03 — Add a scope (site / machine / instance) to the event contract

| | |
| --- | --- |
| Complexity | Low–Medium |
| Effort | 1–2 person-days |
| Depends on | 01 |
| Blocks | 04, 05, 06 |

## Goal

Every envelope states which level owns the fact: `site`, `machine`, or
`instance`. Scope is what later steps route on — a site-scoped event is
distributed to every machine, a machine-scoped event reaches the machine
store, an instance-scoped event stays in the local record.

**Scope only ever adds destinations.** Every event a process states is also
appended to that instance's event log, whatever its scope. The log is the
instance's operational record — a logfile whose entries are an explicitly
declared set, namely everything that happened on this instance — so a
machine-scoped or site-scoped fact stays in it as well as travelling. Nothing
is routed away from the local record, and the JSONL backend therefore has no
scope configuration.

## Design

Follow the contract's existing pattern: everything not stated is defaulted,
and an event that differs from a default implements one small optional
interface (`Versioned`, `Severe`, `Tagged`, `Identified` already work this
way).

1. New closed type in `internal/events`:

   ```go
   type Scope string

   const (
       ScopeSite     Scope = "site"
       ScopeMachine  Scope = "machine"
       ScopeInstance Scope = "instance"
   )
   ```

   with `Scope.Valid()` and `DefaultScope = ScopeInstance`. Instance is the
   right default because it is the harmless direction: a forgotten
   declaration keeps a fact local instead of leaking it site-wide.

2. Optional interface on the event, mirroring `Severe`:

   ```go
   type Scoped interface{ Scope() Scope }
   ```

   `Factory.Wrap` resolves it exactly like `severityOf` does: default when
   absent, reject an invalid value with `ErrInvalidEvent`.

3. `Envelope` gains a required `scope` JSON field, validated by
   `Envelope.Validate`. Bump nothing else; the envelope is self-describing
   and existing JSONL files simply predate the field (decide in review
   whether `Decode` treats a missing scope as `instance` for old files or
   rejects it — recommended: default it, the local record is operator
   evidence, not replayed state).

   **Decided: default it.** `Decode` reads a scopeless object as `instance`
   and still rejects an unknown scope, which is corruption rather than
   history.

4. **Safety net at composition, not just convention.** The default-instance
   choice means a site event whose author forgot `Scope()` would silently
   never leave the machine. Counter this where publishers are composed
   (`internal/app` and later the per-level stores): the site publisher
   refuses a non-site-scoped event, the machine store refuses a
   non-machine-scoped envelope. A misdeclared event then fails loudly on its
   first publication in tests.

   **Deferred to step 05, and replaced for now by a catalog test.** There is
   no publisher to hang the check on yet: site composition is a stub
   (`app.open` returns `ErrNotImplemented`) and the machine store is step 05's
   to build, so a scope-checking publisher written today would be unreachable
   code that `task deadcode` fails on. The net that exists instead is per
   catalog: `registration`'s test requires every event it declares to
   implement `Scoped` and return `ScopeSite`, so a new registration event that
   forgets the declaration fails before it is ever published. `redundancy`'s
   table asserts a scope per event, and `app`'s stamps every event through a
   real factory and asserts the default resolved to `instance`. Add the
   composition guard in step 05, when there is a store for it to protect.

5. Classify the three existing catalogs and declare scopes:

   | Catalog | Scope | Note |
   | --- | --- | --- |
   | `internal/site/registration` (`proposed`, `confirmed`, `rejected`, `accepted`) | `site` | the whole point of the domain |
   | `internal/machine/redundancy` (ownership, activation) | mixed, see open question — **decided: option (a)** | today they reach only the writer's local JSONL |
   | `internal/app` (`platform.app.*`: process, API, epoch, standby, projection) | `instance` | facts about one process |

6. Update `internal/events/doc.go` ("Where an event goes" section) and the
   Events section of `docs/01-architecture.md`.

## Acceptance criteria

- All envelopes stored by existing paths carry a valid scope; JSONL decode
  round-trips it; `go test ./...` green.
- A table in the code (each catalog's events.go header) states the scope of
  every event the package declares.
- A deliberately misdeclared event in a composition test is rejected by the
  scope check, proving the safety net works.

## Risks / open questions

- **Redundancy events stated by the passive instance.** `StandbyWaiting`,
  lease-open failures, and similar facts are stated by an instance that, per
  step 05, must not write to the machine store. Options: (a) those specific
  events stay `instance`-scoped and only owner-stated facts
  (`ownership_acquired`, activation transitions) are `machine`-scoped;
  (b) all redundancy events stay `instance` until a machine-level consumer
  exists. Recommendation: (a), decided per event during this step, recorded
  in the catalog header. This is open question 2 in the README.

  **Decided: (a).** `ownership_acquired`, `stepped_down`, `failback_initiated`,
  and the three `activation_*` events are `machine`; `lease_opened`,
  `ownership_waiting`, `promotion_declined`, and `lease_renewal_failed` are
  `instance`. The line is who the fact is about, not who stated it: an owner's
  transitions outlive the process that made them and are what the machine's
  other instance has to agree with, while a passive instance's waiting and
  declining are stated by the very instance step 05 forbids from writing to the
  machine store. The table is in `redundancy/events.go`'s header.
- Scope vs source overlap: source (`registration`, `redundancy`, `app`) is
  derived from the type; scope could in principle be derived from a
  source→scope table. Rejected: it would make `internal/events` know every
  domain, which its doc.go forbids ("It owns no events of its own"). Scope
  is declared by the owning catalog like severity is.
