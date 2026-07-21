# Phase 5: Implementation backlog

Section 10 of the required output. Prioritized. Complexity S, M, L.

Ordering within an epic is dependency order. Across epics, the epic order is the
implementation order.

---

## Epic 1: Ownership abstraction

Goal: make the primitive replaceable without touching consumers.

| ID | Feature | Task | Cx | Depends | Acceptance criteria |
| --- | --- | --- | ---: | --- | --- |
| 1.1 | Ownership interface | Extract an ownership interface from `Fence`, keeping the current five-method surface | S | none | `Fence` delegates; every consumer compiles unchanged; `task all` passes |
| 1.2 | Acquisition result | Add a result type carrying cause (initial, promotion, reclamation) and abandonment | S | 1.1 | File-lock implementation always reports not-abandoned; nothing consumes it yet |
| 1.3 | Conformance suite | One shared test suite any implementation must pass | M | 1.1 | Covers acquire, contend, release, idempotent release, cancellation, cross-process exclusion; file lock passes |
| 1.4 | Service account model | Document which identities run primary and standby | S | none | Written and reviewed; sufficient to design a DACL |
| 1.5 | Name derivation | Define mutex name from deployment identity, with sanitization and length bound | S | 1.4 | Deterministic; collision-free across deployments on one machine; documented |
| 1.6 | Affinity constraint | Record thread affinity as a correctness constraint in package docs | S | none | States the hazard, the mitigation, and the consequence of violating it |

---

## Epic 2: Named mutex implementation

Goal: a tested implementation nothing uses yet.

| ID | Feature | Task | Cx | Depends | Acceptance criteria |
| --- | --- | --- | ---: | --- | --- |
| 2.1 | Pinned ownership thread | Dedicated OS thread, pinned for process lifetime, performing all mutex operations | **L** | 1.1 | All operations occur on one thread; thread exits only at process exit |
| 2.2 | Create-or-open | Create-or-open in `Global\`, detecting which occurred | M | 2.1, 1.5 | Creation sets the DACL; opening an existing object is reported distinctly |
| 2.3 | Security descriptor | Explicit DACL granting only the Epic 1.4 identities | M | 2.2, 1.4 | Resulting descriptor is asserted, not assumed; no world access |
| 2.4 | Namespace enforcement | `Global\` only, no fallback, no configuration | S | 2.2 | No code path reaches `Local\`; a test proves it |
| 2.5 | Kernel wait | Wait over mutex plus cancellation event | M | 2.1 | No polling; cancellation returns promptly without acquiring |
| 2.6 | Abandonment | Surface abandonment through the acquisition result | S | 2.5, 1.2 | A killed owner produces an abandoned acquisition in the next acquirer |
| 2.7 | Affinity test | Test that ownership survives migration or exit of the calling task | **L** | 2.1 | Fails against a naive non-pinned implementation |
| 2.8 | Squat detection | Fail startup with a diagnosable error on an object the platform did not create | M | 2.2 | Error names the condition; does not proceed on an unvalidated handle |
| 2.9 | Privilege failure | Fail fast and specifically when `Global\` is unavailable | S | 2.4 | Error distinguishes missing privilege from general access denial |
| 2.10 | Conformance | Pass the Epic 1.3 suite | M | 2.1 to 2.6 | Identical suite passes for both implementations |

2.1 and 2.7 are the two tasks that decide whether this migration is safe. Neither
should be compressed.

---

## Epic 3: Transition release

Goal: mixed-version safety.

| ID | Feature | Task | Cx | Depends | Acceptance criteria |
| --- | --- | --- | ---: | --- | --- |
| 3.1 | Dual acquisition | Acquire mutex then file lock; release in reverse | M | 2.10 | Either failure means not owner; ordering is tested |
| 3.2 | Temporary marker | Comment the shim as temporary, naming the retirement phase | S | 3.1 | A reader knows it is scheduled for removal and when |
| 3.3 | Ownership events | Emit the operational events from the target architecture | S | 3.1 | Events name which primitive was refused |
| 3.4 | Mixed-version scenario | Dual-primitive process against file-lock-only process | **L** | 3.1 | Exclusion proven in both start orders; this gates the release |

---

## Epic 4: Failover

Goal: remove the poll.

| ID | Feature | Task | Cx | Depends | Acceptance criteria |
| --- | --- | --- | ---: | --- | --- |
| 4.1 | Kernel wait in standby | Replace `filelock.Acquire` in `runStandby` and `awaitFence` | M | 3.4 | No poll interval on the promotion path |
| 4.2 | Contested file lock | Handle winning the mutex but not the file lock | M | 4.1 | Reported as an old peer still active, not an unexpected error |
| 4.3 | Abandonment in status | Record crash-versus-planned in status and events | S | 2.6 | An operator can tell whether a failover was planned without correlating logs |
| 4.4 | Failover measurement | Measure promotion and handover against the baseline | S | 4.1 | Recorded with sample count and host; no SLO claimed |

---

## Epic 5: Switchover and upgrade

Goal: controlled switchover documented, rolling upgrade delivered.

| ID | Feature | Task | Cx | Depends | Acceptance criteria |
| --- | --- | --- | ---: | --- | --- |
| 5.1 | Switchover runbook | Document the procedure, derived from the scenario | M | 4.1 | Preconditions are machine-checkable |
| 5.2 | No-overlap scenario | Assert the old owner is not active before the new one is | **L** | 5.1 | Observes both processes across the transition with comparable timestamps |
| 5.3 | Rolling upgrade runbook | Per-machine procedure built on 5.1 | M | 5.1 | Interruption bounded by one switchover |
| 5.4 | Single-process limit | Document that `standby.disabled = true` cannot roll | S | 5.3 | Stated in the runbook, not discovered in a maintenance window |
| 5.5 | Storage sequencing | Site-level ordering preserving journal quorum | M | 5.3 | Storage machines one at a time, with a health gate between |
| 5.6 | Upgrade scenario | End-to-end upgrade of a standby-enabled machine | **L** | 5.3 | Service continuity asserted across the switchover |

---

## Epic 6: Retirement

Goal: the mutex is the only primitive. **Gated on fleet state.**

| ID | Feature | Task | Cx | Depends | Acceptance criteria |
| --- | --- | --- | ---: | --- | --- |
| 6.1 | Fleet gate | Confirm every machine runs at least the Epic 3 release | S | 3.4 | Explicit confirmation recorded before any code change |
| 6.2 | Remove dual acquisition | Drop the file lock from `Fence` | S | 6.1 | Only the mutex remains |
| 6.3 | Delete `utils/filelock` | Remove the package and its tests | S | 6.2 | Package gone; `task deadcode` clean |
| 6.4 | Remove fence path | Drop `FenceFileName` and fence path derivation; keep `StatusPath` | S | 6.3 | Status files unaffected |
| 6.5 | Documentation sweep | Remove the local-filesystem ownership requirement; rewrite troubleshooting | M | 6.4 | No reference to `active.lock` survives anywhere |

---

## Epic 7: Windows-only

Goal: remove unreachable Linux code. Detail in `06-windows-only.md`.

| ID | Feature | Task | Cx | Depends | Acceptance criteria |
| --- | --- | --- | ---: | --- | --- |
| 7.1 | atomicfile | Remove `replace_unix.go`, fold the Windows path in | S | none | No build tag remains |
| 7.2 | processinfo | Remove `resident_linux.go`, `resident_other.go`, and their tests | S | none | Windows implementation only |
| 7.3 | processtree | Remove `owner_unix.go` | S | none | Windows implementation only |
| 7.4 | Harness GOOS | Remove both `runtime.GOOS` branches in `scenarios/harness_test.go` | S | none | `.exe` is unconditional |
| 7.5 | Builder GOOS | Pin `GOOS=windows`; remove `effectiveGOOS` branching | M | none | Cross-compilation surface removed; packages still build |
| 7.6 | Unix dependency | Remove `golang.org/x/sys/unix` from the module graph | S | 6.3, 7.1 to 7.3 | `task tidy` produces no unix dependency |
| 7.7 | Docs | Remove Linux CI promises | S | none | `docs/backlog/redundancy.md:29`, `docs/operations/monitoring.md:93` corrected |

---

## Epic 8: Hardening

Goal: prove it, and close the Windows Service gap.

| ID | Feature | Task | Cx | Depends | Acceptance criteria |
| --- | --- | --- | ---: | --- | --- |
| 8.1 | Failover matrix | Kill at every lifecycle state including mid-activation and mid-release | **L** | 6.5 | Deterministic; no state produces two actives |
| 8.2 | Affinity under load | Assert affinity under sustained work | M | 2.7 | Ownership never abandoned while the process lives |
| 8.3 | Squat scenario | Foreign process holds the name | M | 2.8 | Platform reports it diagnosably and does not become active |
| 8.4 | Privilege scenario | `Global\` unavailable | S | 2.9 | Fails fast with a specific error |
| 8.5 | **Windows Service integration** | SCM integration and graceful stop | **L** | none | Service stop produces a clean release, not an abandonment |
| 8.6 | Percentiles | Measure on more than one host | M | 8.1 | Sample count and host stated; no SLO claimed |

**8.5 is a feature, not hardening.** It is listed here because this analysis
discovered it, and it may warrant its own plan. `app.go:54` handles `os.Interrupt`
and `syscall.SIGTERM` with no SCM integration anywhere in the repository. Do not
let its size hide inside a hardening epic.

---

## Priority summary

| Priority | Items | Why |
| --- | --- | --- |
| P0 | 2.1, 2.7 | Thread affinity decides whether the approach is safe at all |
| P0 | 3.1, 3.4 | Mixed-version exclusion; without it the migration causes the failure it prevents |
| P0 | 6.1 | The fleet gate; retiring early reintroduces split-brain |
| P1 | 1.1 to 1.6, 2.2 to 2.10 | The implementation itself |
| P1 | 4.1 to 4.4 | The latency benefit that motivates the change |
| P1 | 8.5 | Production deployment model is absent |
| P2 | 5.1 to 5.6 | Upgrade capability, new and independently valuable |
| P2 | 8.1 to 8.4, 8.6 | Hardening |
| P3 | 6.2 to 6.5, 7.1 to 7.7 | Cleanup, after the gate |
