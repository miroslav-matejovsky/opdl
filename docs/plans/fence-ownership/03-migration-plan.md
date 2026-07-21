# Phase 4: Migration strategy

Section 9 of the required output. Nine phases. The suggested eight, plus the
Windows-only consolidation, which is placed where it costs least.

Each phase carries goal, scope, components, risks, validation, rollback, and
definition of done.

## Ordering constraint that shapes everything

An old process using the file lock and a new process using the mutex do not
exclude each other. Two actives on one machine is the failure the fence exists to
prevent.

Therefore: **the transition release holds both primitives, and the file lock is
retired only after the whole fleet has passed through that release.** Phase 7 is
gated on fleet state, not on code readiness. This is the single most important
sequencing rule in the plan.

---

## Phase 1: Architecture preparation

**Goal.** Make the ownership primitive replaceable without touching any consumer,
and record the decisions that the implementation depends on.

**Scope.** No behavior change. No primitive change.

- Introduce an ownership abstraction inside `platform/internal/redundancy` that
  `Fence` delegates to. The existing five-method surface stays.
- Extend the acquisition result to carry a cause (initial, promotion,
  reclamation) and an abandonment indication. The file-lock implementation always
  reports "not abandoned"; nothing consumes it yet.
- Write the service account model down. Which identities run primary and standby.
  This does not exist today and the DACL cannot be designed without it.
- Decide and document the mutex name derivation, sanitization, and length bound.
- Record the thread-affinity requirement in the package documentation as a
  correctness constraint, not a note.

**Components.** `platform/internal/redundancy`, `docs/operations/deployment.md`.

**Risks.** Low. The main one is premature abstraction: designing the interface
around the mutex before writing it, and discovering the shape is wrong. Mitigate
by keeping the interface exactly the current `Fence` surface plus the cause.

**Validation.** `task all` passes unchanged. No behavioral test changes.

**Rollback.** Revert. Nothing deployed behaves differently.

**Done.** The abstraction exists, the file lock sits behind it, all existing tests
pass untouched, and the account model and naming scheme are written down and
reviewed.

---

## Phase 2: Mutex ownership infrastructure

**Goal.** A tested named-mutex ownership implementation that nothing uses yet.

**Scope.**

- New implementation behind the Phase 1 abstraction.
- Dedicated OS thread, pinned for process lifetime, performing every acquire,
  wait, and release. This is the correctness centerpiece.
- `Global\` namespace only. No `Local\` fallback and no configuration option.
- Explicit DACL at creation. Create-versus-open detection with an error on an
  object the platform did not create.
- Kernel wait over the mutex and a cancellation event.
- Abandonment surfaced through the acquisition result.

**Components.** New package under `platform/internal/redundancy` or `utils`.

**Risks.**

- **Thread affinity implemented incorrectly.** The highest-severity risk in the
  whole plan, and the failure is silent: it works until a scheduler decision makes
  it not work. Mitigate with a test that acquires, lets the acquiring task migrate
  or the calling goroutine exit, and asserts ownership is still held by the
  process.
- Privilege differences between the developer workstation and the service
  environment mask a `Global\` failure until deployment. Mitigate by testing the
  denial path explicitly.
- DACL construction is easy to get subtly wrong. Mitigate by asserting the
  resulting descriptor rather than assuming it.

**Validation.** Unit tests for acquire, contend, release, idempotent release,
cancellation, and abandonment. A multi-process test, following the pattern in
`redundancy/fence_process_test.go`. An explicit thread-affinity test. A test that
a `Local\` fallback does not exist.

**Rollback.** Delete the package. Nothing depends on it.

**Done.** Both implementations pass an identical shared conformance suite, plus
the mutex-specific tests above. Nothing in the runtime references the new one.

---

## Phase 3: Startup ownership acquisition

**Goal.** The transition release. Both primitives held, so old and new processes
exclude each other.

**Scope.**

- Acquisition order: mutex first, then file lock. Release in the reverse order.
  Mutex first because it is the authority; the file lock is a compatibility shim
  and is documented as such in the code.
- Failure to acquire either means not an owner.
- Emit the ownership operational events from `02-target-architecture.md`.

**Components.** `platform/internal/redundancy/fence.go`,
`platform/internal/app/runtime.go` for events.

**Risks.**

- **Two primitives, two release points, one ordering bug.** A release ordering
  mistake could drop the mutex while the file lock is held, letting a peer in.
  Mitigate by making release strictly reverse-ordered and testing it.
- Diagnostics get harder: two ways to fail to become owner. Mitigate by making the
  operational event name which primitive was refused.
- This phase carries the most temporary complexity of any. It must be explicitly
  temporary in the code comments, with Phase 7 named.

**Validation.** All existing scenarios, especially
`TestWarmStandbyFailoverAndPreferredPrimary`. A new scenario running a
dual-primitive process against a file-lock-only process, asserting they exclude
each other in both start orders. This scenario is the entire justification for the
phase and must exist before it ships.

**Rollback.** Revert to Phase 2 state. Since the release also holds the file lock,
a rolled-back process still excludes a deployed one.

**Done.** The mixed-version exclusion scenario passes in both directions. Failover
measurements are recorded and are no worse than the current baseline.

---

## Phase 4: Failover integration

**Goal.** Remove the poll. Make the mutex the wait mechanism.

**Scope.**

- The standby's wait becomes the kernel wait, not `filelock.Acquire`.
- The file lock is still acquired on promotion, but is no longer waited on. This
  is the point where the two primitives stop being symmetric.
- Record abandonment in status and events so a crash promotion is distinguishable
  from a planned handover.

**Components.** `platform/internal/app/runtime.go` (`runStandby`, `awaitFence`),
`redundancy/status.go`.

**Risks.**

- A process could win the kernel wait and then fail to take the file lock, because
  an old peer holds it. This is not a defect; it means an old peer is still active
  and this process must not proceed. It must be handled explicitly and reported
  clearly, not treated as an unexpected error.
- Removing the poll changes timing across the whole failover path. Measurements
  that were stable may shift, including in ways that expose latent races in
  activation.

**Validation.** Warm-standby scenario, repeated. Forced-kill promotion and planned
handover measured and compared against the baseline in
`docs/01-architecture.md:367`. A scenario asserting a crash promotion is reported
as abandoned and a planned handover is not.

**Rollback.** Restore the poll-based wait. The dual-primitive acquisition stays,
so a rolled-back process is still safe against deployed peers.

**Done.** Promotion latency measurably improves. Abandonment is visible in status
and events. No scenario regresses.

---

## Phase 5: Controlled switchover support

**Goal.** Make planned handover an explicit, documented, tested procedure rather
than a sequence an operator infers.

**Scope.**

- Document the switchover procedure end to end in `docs/operations/`.
- Define preconditions machine-checkably: fresh status, live PID,
  `promotable=true`, empty `last_error`, projection caught up.
- A scenario that performs the full procedure and asserts no overlap.

**Components.** `docs/operations/deployment.md`, a new or extended runbook,
`scenarios/warm_standby_test.go`.

**Risks.** The procedure exists implicitly today and is proven by an existing
scenario. The risk is documenting an idealized version that differs from the
tested one. Mitigate by deriving the runbook from the scenario, not the reverse.

**Validation.** The switchover scenario passes repeatedly. An operator can execute
the runbook against a real two-process machine and reach the same end state.

**Rollback.** Documentation only. No runtime rollback needed.

**Done.** The runbook exists, its preconditions are checkable, and a scenario
proves the procedure including the no-overlap assertion.

---

## Phase 6: Upgrade workflow integration

**Goal.** Deliver rolling upgrade, which does not exist today in any form.

**Scope.**

- Per-machine rolling upgrade procedure built on Phase 5's switchover.
- Explicit statement that a `standby.disabled = true` machine cannot roll and
  takes an outage.
- Site-level sequencing: storage machines one at a time, waiting for the journal
  replica group to be healthy between them.
- Mixed-version guidance for the operator, including the Phase 7 gate.

**Components.** New `docs/operations/upgrade.md`, `docs/README.md`, a scenario.

**Risks.**

- **This is new capability, not a refactor.** It is the largest scope in the plan
  after Phase 2 and the easiest to underestimate.
- Storage-machine sequencing interacts with journal quorum. Upgrading two of three
  storage machines concurrently loses quorum. The runbook must make this hard to
  get wrong.
- An upgrade scenario is expensive to write and slow to run.

**Validation.** A scenario that upgrades a standby-enabled machine end to end with
service continuity asserted across the switchover. Manual validation of the
site-level sequencing on a three-storage-node site.

**Rollback.** Documentation and tooling only. The runtime is unchanged, so a bad
runbook is corrected rather than rolled back.

**Done.** A machine can be upgraded with interruption bounded by one switchover,
proven by a scenario. The single-process limitation and the storage sequencing are
documented.

---

## Phase 7: Lock-file retirement

**Goal.** Remove the file lock entirely. The mutex becomes the sole ownership
primitive.

**Gate.** **Every machine in every deployment must already run at least the Phase
3 release.** This is an operational precondition, not a code one. Shipping this
phase before the fleet has passed through the dual-primitive release reintroduces
mixed-version split-brain. Confirm fleet state before starting.

**Scope.**

- Remove file-lock acquisition and release from `Fence`.
- Delete `utils/filelock` entirely, including `lock.go`, `lock_windows.go`,
  `lock_unix.go`, `doc.go`, and tests.
- Remove `FenceFileName` and the fence path derivation from `redundancy/fence.go`.
  `StatusPath` and the machine directory stay: status files are unaffected.
- Remove the local-filesystem requirement for ownership from
  `docs/operations/deployment.md:60`. `instance_dir` remains needed for status
  files, so its other requirements stay.
- Rewrite `docs/operations/troubleshooting.md:104`, which instructs operators to
  inspect `active.lock`.

**Components.** `utils/filelock` (deleted), `platform/internal/redundancy`,
`docs/operations/*`, `docs/01-architecture.md`.

**Risks.**

- **Premature execution.** If any machine still runs a pre-Phase-3 binary, this
  release does not exclude it. This is the highest-consequence risk in the plan
  and it is operational rather than technical.
- Stale documentation referring to the fence file. Mitigate by grepping for
  `active.lock`, `fence`, and `file lock` across all documentation.

**Validation.** Full scenario suite. A grep proving no reference to the lock file
survives in code or documentation. `task deadcode` confirms nothing orphaned.

**Rollback.** Revert to the Phase 4 state, which restores the dual primitive.
Safe, because a dual-primitive process excludes a mutex-only process through the
mutex.

**Done.** `utils/filelock` no longer exists. No documentation mentions
`active.lock`. All scenarios pass. Ownership depends on no filesystem path.

---

## Phase 8: Windows-only consolidation

**Goal.** Remove Linux and Unix-specific code, which is unreachable in a
Windows-only product and is pure cognitive load.

**Placed here deliberately.** After Phase 7, because retiring the file lock
deletes `lock_unix.go` for free as part of deleting the package. Doing this phase
earlier would mean touching a file that is about to be deleted anyway.

Full inventory, scope, risks, and sequencing are in `06-windows-only.md`.

**Done.** No `!windows`, `linux`, or `unix` build-tagged file remains. No `GOOS`
branch remains. `golang.org/x/sys/unix` is gone from the dependency graph.
Documentation no longer promises Linux CI.

---

## Phase 9: Validation and hardening

**Goal.** Prove the ownership model under adverse conditions, and close the
Windows Service gap.

**Scope.**

- Failover matrix from `05-refactoring.md`, run repeatedly.
- Chaos-style validation: kill at every lifecycle state, including mid-activation
  and mid-release.
- Thread-affinity assertion under sustained load.
- Squatting: a foreign process holds the name, and the platform reports it
  diagnosably.
- Privilege: `Global\` unavailable, and the platform fails fast with a specific
  error.
- **Windows Service integration.** `app.go:54` handles `os.Interrupt` and
  `syscall.SIGTERM`. There is no Service Control Manager integration anywhere in
  the repository, so the Windows Service deployment model the brief requires is
  assumed by documentation and absent from the code. Graceful stop through the
  SCM is what makes controlled switchover and rolling upgrade work in production.
- Percentile measurements, on more than one host, per the standard
  `docs/backlog/redundancy.md` already sets.

**Components.** `scenarios/`, `platform/internal/app`, `docs/operations/`.

**Risks.**

- The Windows Service work is a real feature, not hardening, and is listed here
  only because it is discovered by this plan. It may deserve its own plan. Do not
  let it hide inside a validation phase and get under-scoped.
- Chaos testing at every state is slow and can be flaky. Keep it deterministic per
  `AGENTS.md:74`.

**Validation.** The matrix passes repeatedly. Service stop produces a clean
release, not an abandonment.

**Rollback.** Per item.

**Done.** The matrix passes. SCM stop is graceful. Percentiles are recorded with
sample count and host stated, and no SLO is claimed, per
`docs/backlog/redundancy.md:33`.

---

## Phase dependency graph

```text
1 preparation
  |
2 mutex infrastructure
  |
3 startup acquisition (dual primitive)  <-- transition release, fleet must pass through
  |
4 failover integration (kernel wait)
  |
5 controlled switchover ------+
  |                           |
6 upgrade workflow <----------+
  |
7 lock-file retirement   <-- gated on fleet state, not code
  |
8 windows-only consolidation
  |
9 validation and hardening
```

Phases 5 and 6 are documentation and tooling and can overlap with 4. Phase 7
cannot start until the fleet gate is confirmed. Phase 8 depends on 7 only to avoid
touching a file scheduled for deletion.
