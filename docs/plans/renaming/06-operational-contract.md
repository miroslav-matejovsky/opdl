# Stage 06: Operational event names and runbooks

**Effort:** Small. **Risk:** Medium. **Depends on:** stages 01, 03, 04, 05.

The only stage that breaks an existing operational contract. Ship it last, so the
runbooks are rewritten once against final names.

## Intent

What operations reads must use the same words as the architecture it describes.
Event names, the startup summary, and the three runbooks are the platform's
operational contract, and today they carry the pre-intention vocabulary.

## Current state

### Event names

| Event | Emitted at |
| --- | --- |
| `platform.fence_opened` | `runtime.go`, after opening the ownership object |
| `platform.fence_acquired` | `runtime.go`, initial acquisition and after waiting |
| `platform.fence_waiting` | `runtime.go`, when another instance holds it |
| `platform.fence_open_failed` | `runtime.go`, on open failure |
| `platform.activation_started`, `_completed`, `_failed` | `runtime.go`, carrying the activation kind |
| `platform.status_dir_failed` | `runtime.go` |

### Event attributes

`operations.AttributeObject` (`object`), `AttributeAbandoned` (`abandoned`),
`AttributeExisted` (`existed`), `AttributeActivationKind` (`activation_kind`).

### Runbook consumers

`docs/operations/monitoring.md` event table and derived metrics,
`docs/operations/troubleshooting.md` "Two processes appear active",
`docs/operations/upgrade.md` switchover preconditions and expectations,
`docs/operations/README.md` operational surfaces table.

## Target

| Now | Target |
| --- | --- |
| `platform.fence_opened` | `platform.ownership_opened` |
| `platform.fence_acquired` | `platform.ownership_acquired` |
| `platform.fence_waiting` | `platform.ownership_waiting` |
| `platform.fence_open_failed` | `platform.ownership_open_failed` |
| activation kind `standby promotion` | `failover` (stage 05) |
| activation kind `primary reclamation` | `failback` (stage 05) |
| status field `promotable` | `failover_ready` (stage 05) |

Attribute names stay: `object`, `abandoned`, `existed`, `activation_kind` are all
vocabulary-neutral and already accurate.

### Runbook language

- `docs/operations/upgrade.md` is titled "Switchover and upgrade". "Controlled
  switchover" becomes **Ownership Transfer**; "Reclaiming the preferred primary"
  becomes **Failback**. The Preferred Primary policy should be named where the
  document explains why failback exists.
- `docs/operations/troubleshooting.md` "Two processes appear active" describes the
  condition the architecture prevents. Keep the section; restate it as an ownership
  investigation and use the role and state combinations from stage 02.
- `docs/operations/monitoring.md` gains the role and state table from stage 02 and
  the renamed events.
- `docs/operations/README.md` operational surfaces table already has a "Machine
  fence" row; it becomes "Primary Ownership".

## Why this is a real break

These names are what an operator greps for and what any alert rule matches. A
deployment with alerting on `platform.fence_acquired` goes silent after this
change rather than failing loudly.

Mitigations considered and rejected:

- **Emit both names for a transition period.** Doubles every ownership event and
  leaves two vocabularies in the stream, which is what this plan exists to remove.
- **Keep the old names.** Leaves the most-read surface in the old vocabulary while
  the architecture documentation uses the new one, which is worse than either
  vocabulary used consistently.

Accepted because the project is in an experimentation phase and there is no
deployed fleet, the same basis on which the ownership primitive was replaced
outright. If that changes, this stage needs a deprecation path and the others do
not, which is a further reason to keep it last and separable.

## Decisions

**D1.** Confirm no external alerting depends on the current event names. If any
does, this stage needs a transition plan the others do not.

**D2.** Rename the `platform.` prefix? Recommendation: **no.** It scopes events to
the platform as opposed to the Event Fabric's `event_fabric.` events, and it
carries no vocabulary this plan objects to.

**D3.** Should the startup summary line `instance role=... standby=...` change?
Recommendation: yes, to name the instance role and its service name from stage 01,
since it is the first thing an operator sees in a service log.

## Work

1. Rename the four event names in `platform/internal/app/runtime.go`.
2. Update the startup summary lines in `app.go` and `runtime.go`.
3. Rewrite the four runbook documents against the final vocabulary.
4. Update `docs/operations/README.md` surfaces table.
5. Check `utils/logscan` and any scenario asserting event names by string.

## Validation

- `task all` passes, including scenarios that assert operational events.
- No document under `docs/operations/` uses fence, promotion, or reclamation
  vocabulary.
- Every event name in `docs/operations/monitoring.md` exists in the code, checked
  by grep rather than assumed.
