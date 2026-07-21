# Stage 05: Failover and Failback

**Effort:** Small. **Risk:** Low. **Depends on:** stage 01, and stage 04, whose policy decision determines what failback means.

## Intent

There are two transitions: **Failover** and **Failback**.

**Failover** is the Standby Instance assuming responsibility because Primary
Ownership became unavailable. **Failback** is ownership returning to the Primary
Instance after recovery, upgrade, or maintenance completes, as the Preferred
Primary policy requires.

The codebase calls these **promotion** and **reclamation**.

## Current state

The concepts already exist and are already distinguished, which is why this stage
is small. `platform/internal/app/runtime.go:32` defines three activation kinds:

```go
activationInitial     = "initial activation"
activationPromotion   = "standby promotion"
activationReclamation = "primary reclamation"
```

These map exactly onto the target transitions, plus initial start. The kind is
carried on `platform.activation_started`, `activation_completed`, and
`activation_failed` through `operations.AttributeActivationKind`, so it is
operator-visible.

`promotion`, `promotable`, and `reclamation` appear roughly 114 times across code
and documentation once NATS and journal uses are excluded.

## Target

| Now | Target |
| --- | --- |
| `activationPromotion` = `"standby promotion"` | `activationFailover` = `"failover"` |
| `activationReclamation` = `"primary reclamation"` | `activationFailback` = `"failback"` |
| `activationInitial` = `"initial activation"` | unchanged |
| `Status.Promotable` (JSON `promotable`) | `Status.FailoverReady` (JSON `failover_ready`) |
| "promoted standby" in prose | "the Standby Instance after failover" |
| "returning primary reclaims" in prose | "failback to the Primary Instance" |
| "the primary never steals ownership" | "failback is always an Ownership Transfer, never a seizure" |

Note the activation-kind *values* are operator-visible strings on events. Changing
them is part of the operational contract change; see stage 06, which should ship
with or after this stage so the runbooks are rewritten once.

## The `promotable` decision, carried from stage 02

`Status.Promotable` means "current enough to take over". Under the vocabulary it
is readiness to accept failover.

Renaming it is the widest break in this stage:

| Consumer | Location |
| --- | --- |
| Deployment gating | `docs/operations/deployment.md:132` |
| Handover preconditions | `docs/operations/upgrade.md` preconditions table |
| Acceptance checks | `docs/operations/deployment.md` |
| Monitoring | `docs/operations/monitoring.md` |
| Scenario assertions | `scenarios/warm_standby_test.go` |
| App tests | `platform/internal/app/app_test.go` `waitForPromotableStandby` |
| Runtime | `platform/internal/app/runtime.go` status writes, lag bound |
| Config docs | `platform/internal/config/config.go:146`, `file.go:25` |

Recommendation: rename to `failover_ready`. It is the field an operator checks
before triggering a switchover, so leaving it in the old vocabulary would leave
the most-used field out of step with the runbook that tells them to check it.

If the break is judged too wide, the alternative is to keep `promotable` and
accept one documented exception. That is worse than it sounds: this field appears
in the same table as the renamed transitions, so a reader sees both vocabularies
in one place.

## Decisions

**D1.** Rename `Promotable` to `FailoverReady`? Recommendation above: yes, in this
stage, with all consumers in one change.

**D2.** Activation kind values are strings on events. Change them here or in stage
06? Recommendation: change here, and ship stage 06 with or after this stage so the
runbooks are written once against the final names.

**D3.** `waitForPromotableStandby` and similar test helper names. Recommendation:
rename with the field; they are internal and cost nothing.

## Work

1. Rename the activation kind constants and their values in
   `platform/internal/app/runtime.go`.
2. Rename `Status.Promotable` to `FailoverReady` with JSON `failover_ready` in
   `platform/internal/redundancy/status.go`, and every consumer listed above.
3. Replace promotion and reclamation language in `platform/internal/app/doc.go`,
   `platform/internal/redundancy/doc.go`,
   `platform/internal/eventfabric/nats/doc.go:76`,
   `platform/deployment/deployment.go:193`.
4. Update `docs/01-architecture.md`, and the operations runbooks with stage 06.
5. Leave `docs/backlog/redundancy.md` measurement history alone except for the
   heading words; the recorded numbers are historical facts.

## Validation

- `task all` passes, including `TestWarmStandbyFailoverAndPreferredPrimary`, whose
  name already uses the target vocabulary.
- No status file field or event attribute uses promotion vocabulary.
- The measurement table in `docs/01-architecture.md` labels its rows with the
  target transitions.
