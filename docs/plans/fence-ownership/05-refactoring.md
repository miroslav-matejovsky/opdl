# Phase 6: Refactoring recommendations

Sections 11 and 12 of the required output.

## Exact components that change

Located by reading the code. Grouped by how much they change.

### Deleted

| Path | Note |
| --- | --- |
| `utils/filelock/lock.go` | the poll loop lives here (`:12`, `:58`) |
| `utils/filelock/lock_windows.go` | `LockFileEx` |
| `utils/filelock/lock_unix.go` | `flock`; also removed by the Windows-only phase |
| `utils/filelock/doc.go` | package documentation |
| `utils/filelock/lock_test.go` | tests |

The whole package. It has exactly one consumer.

### Substantially changed

| Path | Change |
| --- | --- |
| `platform/internal/redundancy/fence.go` | `Fence` delegates to an ownership abstraction. `FenceFileName` (`:14`) and `FencePath` (`:26`) are removed at retirement. `machineDir` (`:17`) and `StatusPath` (`:31`) stay: status files keep their layout. |
| `platform/internal/redundancy/doc.go` | The fence description (`:47`) becomes the mutex model. Add the thread-affinity constraint and the precise fencing scope. |

### Lightly changed

| Path | Change |
| --- | --- |
| `platform/internal/app/runtime.go` | `runProcess` (`:61`) unchanged in shape. `runStandby` (`:164`) and `awaitFence` (`:241`) drop the poll for the kernel wait. Ownership events replace fence events (`:68`, `:77`, `:80`). |
| `platform/internal/redundancy/status.go` | Optional abandonment indication, so crash-versus-planned is visible in status. |
| `platform/internal/operations` | New ownership event names and attributes. |
| `platform/internal/config` | Nothing for ownership. `instance_dir` stays for status files but loses its ownership role. |

### New

| Path | Contents |
| --- | --- |
| ownership package | Interface, named-mutex implementation, pinned thread, DACL, name derivation, shared conformance suite |

### Documentation

| Path | Change |
| --- | --- |
| `docs/01-architecture.md:280` | "non-expiring OS file lock" becomes the mutex model |
| `docs/operations/deployment.md:54` | `instance_dir` no longer holds a fence |
| `docs/operations/deployment.md:60` | The local-filesystem requirement for ownership goes; other reasons stay |
| `docs/operations/troubleshooting.md:104` | "Inspect the shared `active.lock`" becomes wrong; rewrite around the owner PID |
| `docs/operations/upgrade.md` | New; does not exist today |
| `docs/backlog/redundancy.md:29`, `docs/operations/monitoring.md:93` | Remove Linux CI promises |

### Tests and scenarios

| Path | Change |
| --- | --- |
| `platform/internal/redundancy/fence_test.go`, `fence_process_test.go` | Retarget at the ownership abstraction; the multi-process pattern is reusable as-is |
| `scenarios/warm_standby_test.go` | Baseline stays; add no-overlap and abandonment assertions |
| `scenarios/harness_test.go` | Remove `runtime.GOOS` branches (`:75`, `:279`) |
| new scenario | Mixed-version exclusion; gates the transition release |
| new scenario | Rolling upgrade |

## Reusable abstractions already present

Worth naming, because the plan depends on them and none needs inventing.

- **`Fence` is already the right seam.** Five methods, one consumer package, no
  leakage of the primitive. The migration is possible in this shape precisely
  because this boundary was drawn correctly the first time.
- **The per-platform file split.** `lock_windows.go` and `lock_unix.go` are the
  existing pattern for platform-specific implementation behind one API. The
  Windows-only phase removes the second half rather than the pattern.
- **`fence_process_test.go`.** A real multi-process exclusion test already exists.
  The hardest test infrastructure for Epic 2 is written.
- **`internal/operations`.** Structured local events independent of the Event
  Fabric (`docs/01-architecture.md:59`), which is exactly right for ownership
  transitions: they are most interesting when the journal is unreachable.
- **The activation-kind concept.** `runtime.go:33` already distinguishes initial
  activation, promotion, and reclamation. The acquisition cause slots into an
  existing vocabulary rather than adding one.
- **The scenario harness.** Builds real packages and drives real processes. The
  failover and upgrade scenarios extend it rather than starting new.

## Ownership abstractions to introduce

1. **An ownership interface**, exactly the current `Fence` surface. Not more. The
   temptation is to design for leases, priorities, or multiple owners; none is
   required and each would add a way to be wrong.
2. **An acquisition result** carrying cause and abandonment. Today acquisition is
   a bool, which is why crash and clean handover are indistinguishable.
3. **A pinned ownership thread**, owned by the implementation and invisible to
   callers. This is the correctness centerpiece and it must not be optional or
   configurable.
4. **A deployment-derived ownership name**, computed from compiled identity, never
   from runtime configuration. This is what makes ownership a machine fact instead
   of a configuration string.
5. **A shared conformance suite** both implementations pass. It is what makes the
   transition release trustworthy: the two primitives are proven interchangeable
   before they are used together.

Deliberately not introduced: a lease, a heartbeat, a preemption path, an operator
override, or a fencing token. The first four break the property that a stalled
owner blocks failover rather than risking two actives. The fifth is unnecessary
while the release-ordering invariant holds, and adding it would suggest the
invariant is optional.

## Lock-file code to remove

| What | Where |
| --- | --- |
| The package | `utils/filelock/` entirely |
| Fence file name | `redundancy/fence.go:14` |
| Fence path derivation | `redundancy/fence.go:26` |
| Lock field on `Fence` | `redundancy/fence.go:49` |
| `filelock.Open` call | `redundancy/fence.go:61` |
| Poll interval | `utils/filelock/lock.go:12` |
| Poll loop | `utils/filelock/lock.go:58` |
| Unix dependency | `golang.org/x/sys/unix`, via `lock_unix.go` |
| Local-filesystem requirement | `docs/operations/deployment.md:60`, `redundancy/fence.go:24` |
| Operator instruction | `docs/operations/troubleshooting.md:104` |

Keep: `machineDir` and `StatusPath` (`fence.go:17`, `:31`). Status files are not
part of this migration and their layout does not change.

## Implementation sequence

Section 12 of the required output.

```text
 1. Ownership interface, acquisition result, conformance suite   Epic 1
 2. Service account model, name derivation                       Epic 1
 3. Pinned ownership thread                                      Epic 2  <- P0
 4. Thread-affinity test                                         Epic 2  <- P0
 5. Create-or-open, DACL, namespace enforcement, squat detection Epic 2
 6. Kernel wait, abandonment                                     Epic 2
 7. Conformance parity between both implementations              Epic 2
 8. Dual-primitive acquisition                                   Epic 3
 9. Mixed-version exclusion scenario                             Epic 3  <- P0 gate
10. Kernel wait on the promotion path                            Epic 4
11. Abandonment in status; failover measurement                  Epic 4
12. Switchover runbook and no-overlap scenario                   Epic 5
13. Rolling upgrade runbook and scenario                         Epic 5
14. FLEET GATE: confirm all machines run step 8                  Epic 6  <- P0 gate
15. Remove dual acquisition; delete utils/filelock               Epic 6
16. Documentation sweep                                          Epic 6
17. Windows-only consolidation                                   Epic 7
18. Failover matrix, squat, privilege, affinity under load       Epic 8
19. Windows Service integration                                  Epic 8
20. Percentiles                                                  Epic 8
```

Three points are gates rather than steps. Step 4 decides whether the approach is
viable at all. Step 9 must pass before the transition release ships. Step 14 is
operational and must be confirmed, not assumed.

Steps 12 and 13 can run in parallel with 10 and 11; they are documentation and
scenarios against behavior that already works.

## Testing strategy

**Conformance first.** One suite, both implementations. Written in Epic 1 against
the file lock, so it encodes current behavior before anything changes rather than
being written to match the new implementation.

**Multi-process, not multi-goroutine.** Ownership exclusion is between processes.
`fence_process_test.go` already does this and is the model.

**Deterministic.** `AGENTS.md:74` requires it. Ownership tests are prone to
sleeps; use the existing `utils/waitfor` and condition-based waiting. Per
`no-require-inside-eventually`, do not call `require.*` inside a polled condition.

**Scenario cache.** Scenarios build the binary at run time. After editing
`platform/**`, re-run with `-count=1` or the change is not exercised.

**Layers:**

| Layer | Covers |
| --- | --- |
| Unit | acquire, contend, release, idempotency, cancellation, name derivation, DACL |
| Multi-process | exclusion, crash release, abandonment, squatting |
| Affinity | ownership survives task migration and calling-task exit |
| Integration | `runProcess` branching, activation kinds, status transitions |
| Scenario | failover, switchover, upgrade, mixed-version |

## Failover testing scenarios

| # | Scenario | Assertion |
| --- | --- | --- |
| 1 | Kill the owner | Standby promotes; acquisition reports abandoned |
| 2 | Graceful stop | Standby promotes; acquisition reports clean |
| 3 | Kill during `activating` | No second active; next start recovers |
| 4 | Kill during release, after resources closed | Waiter acquires; no overlap |
| 5 | Kill the standby | Owner unaffected |
| 6 | Both killed together | Both restart; exactly one becomes owner |
| 7 | Owner stalls, process alive | No failover; ownership retained. Confirms the non-expiring property |
| 8 | Returning primary against a live standby | Waits; does not steal |
| 9 | Repeated failover, ten cycles | No leak of handles or threads; timing stable |
| 10 | Bind fails after acquisition | Failed status with the bind error; no port fallback |
| 11 | Foreign process squats the name | Diagnosable failure; platform does not become active |
| 12 | `Global\` unavailable | Fails fast with a specific privilege error |
| 13 | Owning task exits, process alive | **Ownership retained.** Fails against a non-pinned implementation |
| 14 | Mixed version, old starts first | New process does not become active |
| 15 | Mixed version, new starts first | Old process does not become active |

Scenarios 13, 14, and 15 are the ones this migration adds that have no counterpart
today. They correspond to the two failure modes the mutex introduces: thread
affinity and cross-version primitive mismatch.

## Upgrade testing scenarios

| # | Scenario | Assertion |
| --- | --- | --- |
| 1 | Rolling upgrade, standby-enabled machine | Interruption bounded by one switchover |
| 2 | Upgrade the standby only | Owner undisturbed; standby rejoins promotable |
| 3 | Switchover to the upgraded process | New version owns; site state preserved |
| 4 | Upgrade the former owner, switch back | Preferred primary owns; no registration lost |
| 5 | Single-process machine | Outage occurs and is bounded; documented, not surprising |
| 6 | Interrupted upgrade, new binary fails to start | Old process still owns or reclaims; machine is not left ownerless |
| 7 | Storage machine upgrade on a three-node site | Journal quorum preserved throughout |
| 8 | Two storage machines concurrently | **Quorum lost.** Proves why the runbook sequences them |
| 9 | Mixed-version site across machines | Registration and projection unaffected |

Scenario 8 is a negative test. It should be written to prove the failure the
runbook exists to prevent, so the sequencing rule has evidence behind it rather
than being an assertion in prose.
