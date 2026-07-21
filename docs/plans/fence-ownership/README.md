# Machine fence ownership plan

Evaluate and plan replacing the platform's filesystem-based machine fence with a
Windows Named Mutex as the authoritative ownership primitive.

Nothing here is implemented. Phase 1 is analysis of what exists today.

## Scope

In scope: the local machine fence. Which of the two platform processes on one
machine owns active capabilities.

Out of scope: the site journal, registration, Event Fabric transport, inter-node
coordination, and client-service leadership (planned separately in
`docs/plans/leadership/`).

## Recommendation, up front

**Adopt the named mutex, conditional on three preconditions. If any cannot be met,
keep the file lock.**

1. **Thread affinity is guaranteed.** A dedicated OS thread, pinned for the life
   of the process, performs every acquire, wait, and release.
2. **`Global\` is mandatory and validated at startup.** No `Local\` fallback, no
   configuration option, fail fast when unavailable.
3. **The transition preserves mutual exclusion across versions.** One release
   holds both primitives; the file lock is retired only after the fleet passes
   through it.

The decisive argument is not latency. It is that **ownership identity should be a
machine fact, not a configuration string.** The three unprevented split-brain
scenarios in the current design all reduce to the fence being scoped by a path
that two processes must agree on and that nothing validates.

## The three findings that shape this plan

### 1. A named mutex is owned by a thread, not a process

This is the most important technical finding and it cuts against the change.

A file lock is held by a process through a handle. A Windows mutex is held by the
*thread* that acquired it: if that thread exits while the process lives, the mutex
is abandoned and another process can acquire it while the first still holds its
sockets, its embedded server, and its storage. That is a split-brain vector the
file lock structurally cannot have, and in a runtime that migrates work across OS
threads it is easy to hit by accident.

The mitigation is mandatory, not advisory. It is condition 1 above, and it is why
the recommendation is conditional rather than a formality.

Note also what is *not* different: `LockFileEx` is already kernel-managed
exclusion. The current design does not manage ownership in user space. The change
is the object's namespace and its affinity, not the introduction of the kernel.

### 2. Changing the primitive is itself a split-brain risk

An old process using the file lock and a new process using the mutex do not
exclude each other. They contend for different objects and both become active.

This is not a property of the mutex; it is a property of any primitive change. It
forces a dual-primitive transition release, and it makes lock-file retirement an
operationally gated phase rather than a code cleanup. A configuration flag
selecting the primitive is rejected outright: it promotes the failure to a
supported option.

### 3. There is no upgrade procedure and no Windows Service integration

A repository-wide search for upgrade, rolling, or switchover returns nothing: no
procedure, no documentation, no code path, no test. The brief requires rolling
upgrades with minimal interruption. That capability has to be built, and it is
independent of which ownership primitive is chosen.

Separately, `platform/internal/app/app.go:54` handles `os.Interrupt` and
`syscall.SIGTERM`, and there is no Service Control Manager integration anywhere.
The Windows Service deployment model the brief requires is assumed by the
documentation and absent from the code. Graceful SCM stop is what makes controlled
switchover and rolling upgrade work in production, so this is a prerequisite for
the brief's goals rather than a detail.

## Current architecture in one picture

```text
                machine
   +-------------------------------+
   |  primary process              |
   |    -instance primary          |
   |  standby process              |
   |    -instance standby          |
   +-------------------------------+
                  |
        both compute the same path
                  |
   <instance_dir>/<proj>-<env>-<site>-<machine>/active.lock
                  |
        LockFileEx exclusive, non-expiring
                  |
        holder composes: HTTP listener, embedded NATS,
        JetStream storage, durable handlers, readiness
        waiter composes: client-only projector, status
```

Ownership is one non-blocking attempt at startup. The waiter polls every 100 ms
(`utils/filelock/lock.go:12`). Release happens only after active resources close
(`platform/internal/app/runtime.go:290`), which is what makes the shared endpoint
set safe.

## Target in one picture

```text
                machine
   +-------------------------------+
   |  primary process              |
   |  standby process              |
   +-------------------------------+
                  |
    name derived from compiled deployment identity
                  |
        Global\<project-env-site-machine>
        explicit DACL, created not inherited
                  |
        acquired on a dedicated pinned OS thread
        kernel wait, no polling
        abandonment reported on crash
                  |
        holder composes: unchanged
        waiter composes: unchanged
```

Everything below the ownership primitive is unchanged: transfer ordering, shared
endpoints, lifecycle states, process roles, status files, and the non-expiring
property that makes a stalled owner block failover rather than risk two actives.

## What the mutex fixes, and what it costs

| Vector | File lock | Mutex |
| --- | --- | --- |
| Simultaneous start | prevented | prevented |
| Crash | prevented | prevented, and signalled |
| Stall or pause | prevented | prevented |
| Network filesystem | **not prevented** | not applicable |
| Config mismatch between processes | **not prevented** | prevented |
| Duplicate install under another path | **not prevented** | prevented |
| Owning thread exits, process alive | not applicable | **not prevented without pinning** |
| Mixed-version processes | n/a | **not prevented without a dual-primitive release** |
| Promotion latency floor | 100 ms poll | none, kernel wait |
| Operator visibility | `dir` shows the file | needs Process Explorer or `handle.exe` |

Net: fewer ways to misconfigure, more ways to misunderstand.

## On the mutex as fencing authority

The brief asks the mutex to be the ownership *and* fencing authority. It is the
first without qualification. The second needs precision, because overstating it
puts weight on the primitive that it cannot carry.

A mutex provides mutual exclusion, not a fencing token. In this design that gap is
already closed by construction: ownership is released only after active resources
close, a crashed process's resources die with it, and a stalled owner keeps
ownership so no second owner exists. The residual case is exactly the
thread-affinity hazard, which condition 1 prevents.

**Fencing here is achieved by the release-ordering invariant plus thread pinning,
and the mutex is the ownership authority that makes those two sufficient.** If the
ordering invariant is ever weakened, an explicit monotonic ownership generation
becomes necessary and the mutex will not substitute for it. This should be written
into the package documentation so a later change cannot quietly assume otherwise.

## Documents

| File | Required output sections |
| --- | --- |
| [00-current-state.md](00-current-state.md) | 1 Current State Analysis, 2 Findings, 3 Failure Analysis |
| [01-assessment.md](01-assessment.md) | 4 Filesystem Lock Assessment, 5 Windows Mutex Assessment, 6 Recommendation |
| [02-target-architecture.md](02-target-architecture.md) | 7 Target Architecture, 8 Ownership State Model |
| [03-migration-plan.md](03-migration-plan.md) | 9 Migration Plan |
| [04-backlog.md](04-backlog.md) | 10 Backlog |
| [05-refactoring.md](05-refactoring.md) | 11 Refactoring Recommendations, 12 Implementation Order |

## Migration at a glance

```text
1 preparation
2 mutex infrastructure           <- thread affinity decides viability
3 startup acquisition            <- dual primitive; fleet must pass through
4 failover integration           <- the poll is removed here
5 controlled switchover
6 upgrade workflow               <- new capability, not a refactor
7 lock-file retirement           <- gated on fleet state, not code
8 validation and hardening       <- includes Windows Service integration
```

Three gates rather than steps: the thread-affinity test (does the approach work at
all), the mixed-version exclusion scenario (may the transition ship), and the
fleet confirmation (may the file lock be deleted).

## Why the blast radius is small

`Fence` is already the right seam: five methods, one consumer package, no leakage
of the primitive. `utils/filelock` has exactly one consumer. A real multi-process
exclusion test already exists in `redundancy/fence_process_test.go`.

The migration is feasible in this shape because that boundary was drawn correctly
the first time. Most of the plan's risk is in the two new failure modes, not in
the refactor.

## The repository is Windows-only

A premise of this plan, not work inside it. Every `go:build` directive and
`runtime.GOOS` branch has been removed, the builder pins `GOOS=windows` rather
than taking it from the host, and no documentation promises Linux validation.

This is what makes a Windows-native ownership primitive a consistent choice
rather than a portability regression.
