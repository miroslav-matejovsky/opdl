# Stage 02: Active and Passive runtime states

**Effort:** Small. **Complexity:** Low. **Depends on:** stage 01.

## Intent

A runtime state is **Active** or **Passive**. The word `standby` names a role and
nothing else.

## Why this is not cosmetic

`standby` is currently both a role and a state, and both appear in one status file:

```json
{"role": "standby", "state": "standby"}
```

An operator reading that cannot tell which axis either field is on, and the two
mean different things: the first is a build-time decision that never changes, the
second is where the process is right now. The pair `role=primary, state=standby`
is the one that causes real confusion, because it looks like a contradiction and is
in fact the normal resting state of a Primary Instance that has not failed back.

With Passive, both become unambiguous: `role=primary, state=passive` reads as what
it is.

## Current state

`platform/internal/redundancy/state.go` defines the state machine:

```go
StateStarting   State = "starting"
StateStandby    State = "standby"     // -> passive
StateActivating State = "activating"
StateActive     State = "active"
StateStopping   State = "stopping"
StateFailed     State = "failed"
```

`StateStandby` is the only value that changes. The transition table at
`state.go:46-49` references it in three places.

Consumers: the status file written by `platform/internal/app/runtime.go`, the
scenario assertions that poll for a state string, `redundancy.Status`, and the
operations runbooks.

## Target

| Now | Target |
| --- | --- |
| `StateStandby` | `StatePassive` |
| `"standby"` as a state value | `"passive"` |
| `role=standby, state=standby` | `role=standby, state=passive` |
| "the standby is standing by" in prose | "the Standby Instance is Passive" |

The role value `standby` is unchanged. That is the point of the stage.

## The hazard this stage carries

This is a rename of a word that appears on both axes, so a global search and
replace will corrupt the role.

The procedure that worked last time this codebase renamed a load-bearing word:
sed the **identifiers** only (`StateStandby`, `StatePassive`), replace prose with
explicit before-and-after pairs, then grep the new word and read every hit. A
word-boundary sed over comments compiles cleanly and produces text that reads as
English and means nothing. The compiler cannot catch it and neither can `task all`.

Budget for the reading. There is no shortcut.

## Decisions

**D1.** Is `state=passive` a breaking change for anything outside the repo?
Scenario assertions and runbooks are in-repo and will be updated with it. If any
external alerting matches on `"state":"standby"`, it breaks silently. Confirm
before landing.

**D2.** Does the status file need a schema version so a reader can tell the two
encodings apart? Recommendation: not for this alone, but note it if stage 04 also
changes the file.

## Work

1. Rename `StateStandby` to `StatePassive` with value `passive` in
   `platform/internal/redundancy/state.go`, including the transition table.
2. Update every consumer: status writes in `platform/internal/app/runtime.go`,
   `redundancy.Status`, scenario assertions, app tests.
3. Replace prose in `platform/internal/redundancy/doc.go` and
   `platform/internal/app/doc.go` with explicit pairs, then grep and read.
4. Update the role/state table in `docs/operations/monitoring.md`, which is the
   one place that table lives, and the states named in
   `docs/operations/troubleshooting.md` and `docs/operations/upgrade.md`.

## Validation

- `task all` passes with the gate from stage 01 enabled.
- No status file or event carries `standby` as a state value.
- `grep -rn 'state.*standby'` returns nothing that means a state.
- The monitoring runbook's role/state table lists `passive` and still explains
  that `role=primary, state=passive` is a steady state, not a transient one.
