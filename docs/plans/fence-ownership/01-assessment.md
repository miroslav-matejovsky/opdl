# Phase 2: Architecture evaluation

Sections 4, 5, and 6 of the required output.

## 4. Filesystem lock assessment

### Benefits

- **Correct today.** Exclusivity is enforced atomically by the kernel through
  `LockFileEx` with `LOCKFILE_EXCLUSIVE_LOCK | LOCKFILE_FAIL_IMMEDIATELY`. The
  primitive itself is not the source of any known defect.
- **Crash-released with no cleanup logic.** The OS drops the lock when the handle
  closes. There is no stale-lock detection, PID check, timeout, or reaper to get
  wrong. A whole class of bugs is absent by construction.
- **Non-expiring, so a stalled holder blocks failover instead of risking two
  actives** (`fence.go:44`). This is the correct trade and it is a property, not a
  limitation.
- **Ownership is process-affine.** The lock is held on a file handle owned by the
  process. No thread lifetime is involved. This matters greatly in the comparison.
- **Directly observable.** The path is documented, greppable, and inspectable with
  ordinary tools. `troubleshooting.md:104` sends operators to it.
- **Portable.** One abstraction over `LockFileEx` and `flock` lets the same code
  and the same tests run on Windows and Linux.

### Weaknesses

- **Polling.** `lock.go:12` polls every 100 ms. There is no kernel wait and no
  notification on release, so up to 100 ms is added to every promotion and
  handover against measured promotions of 126 to 182 ms.
- **Ownership identity is a configuration string.** The fence is scoped by path
  (`fence.go:27`). Two processes exclude each other only if they agree on
  `instance_dir`, and nothing verifies that they do.
- **Filesystem dependency is a liveness dependency.** A status write failure stops
  the active process (`runtime.go:115`), and the fence requires a writable
  directory for the process's life.
- **Network filesystem hazard.** Lock semantics over SMB or NFS are not
  equivalent. The mitigation is a sentence in a runbook (`deployment.md:60`).
- **Security rests on ACLs the platform does not set.** The fence directory
  inherits whatever `instance_dir` was provisioned with. Unix mode bits in
  `lock.go:25` are largely advisory on Windows.
- **Local denial of service.** Any local process with write access to the path can
  hold the lock and prevent the platform from ever becoming active.

### Failure scenarios

Covered in `00-current-state.md`. The ones that discriminate against this approach:
`instance_dir` on a network filesystem, `instance_dir` mismatch between the two
processes, and the same machine package installed twice under different paths. All
three are undetected, and all three produce two simultaneous actives.

### Recovery behavior

Implicit and reliable. The OS releases on handle close; the waiter's next poll
succeeds. The file is never deleted and its content is never read, so there is no
stale state to reconcile.

The one gap: nothing distinguishes a crash from a clean release. The waiter
acquires identically in both cases and cannot report which happened.

### Upgrade impact

Neutral, because no upgrade procedure exists. A same-primitive rolling upgrade
would work: an old and a new binary both using `LockFileEx` on the same path
exclude each other correctly. This is a real advantage over any migration that
changes the primitive, and it is the central constraint on Phase 4.

### Split-brain risks

Low within its assumptions and unbounded outside them. Scenarios 1 through 5 in
`00-current-state.md` are prevented. Scenarios 6, 7, and 8 are not prevented and
share one root cause: a path is not a machine.

### Operational complexity

Low and well understood. One directory to provision, one prohibition to respect,
one file to inspect. The runbook is short and correct.

## 5. Windows Named Mutex assessment

### Benefits

- **Machine-scoped identity, not path-scoped.** A `Global\` named mutex is a
  kernel object in a machine-wide namespace. Two processes contend for the same
  object because they use the same name, regardless of installation path, working
  directory, or `instance_dir`. This eliminates split-brain scenarios 6, 7, and 8
  outright rather than documenting them away.
- **Kernel wait, no polling.** `WaitForSingleObject` blocks in the kernel and
  wakes on release. The 100 ms poll floor disappears from every promotion and
  handover.
- **Clean cancellation.** `WaitForMultipleObjects` over the mutex and a
  cancellation event gives context-aware waiting without a poll loop, which is
  what the poll partly exists to provide today.
- **No filesystem dependency for ownership.** Ownership survives a read-only or
  failing volume. The fence stops being coupled to disk health.
- **Explicit abandonment signal.** `WAIT_ABANDONED` tells the acquirer that the
  previous owner died without releasing. The current design cannot distinguish a
  crash from a clean handover; this makes crash promotion and planned handover
  separately observable.
- **Explicit security descriptor.** A DACL can be set at creation, so access is
  something the platform states rather than something it inherits.
- **Preserves the non-expiring property.** A mutex has no lease. A stalled holder
  keeps it, so scenario 3 behaves exactly as it does today.

### Weaknesses

These are the reasons this evaluation is not a formality. Each is a real hazard
that the file lock does not have.

**Mutex ownership is thread-affine, not process-affine.** This is the most
important finding in Phase 2. A Windows mutex is owned by the *thread* that
acquired it. If that thread exits while the process keeps running, the mutex is
abandoned and another process can acquire it while the first still holds its
sockets, its embedded server, and its storage. That is a split-brain vector the
file lock structurally cannot have.

Any runtime that multiplexes work across OS threads makes this easy to hit by
accident. The mitigation is mandatory and must be treated as a correctness
requirement: **a dedicated OS thread acquires the mutex, is pinned for the life of
the process, does nothing else, and exits only at process exit.** In a language
with lightweight tasks that migrate between OS threads, this means an explicit
thread-pinning primitive around every mutex operation. If the implementation
language cannot guarantee thread affinity, this approach must not be adopted.

**Namespace and privilege.** `Local\` names are per-session. A Windows Service in
session 0 and an interactive process in another session would each get their own
object and would not exclude each other. `Global\` is therefore mandatory, and
creating a `Global\` object requires `SeCreateGlobalPrivilege`, which services and
administrators hold by default and ordinary interactive users do not. Running the
platform interactively during development becomes a privileged operation or a
different code path, and a different code path here is a way to test something
other than what ships.

**Name squatting.** Any local process can create a named object first. If it
creates the platform's mutex name and holds it, the platform never becomes active:
a local denial of service, equivalent to the file-lock case. Worse, a squatter
creating the object first also chooses its DACL, so the platform may open an
object whose access control it does not own. The platform must therefore detect
that it opened rather than created the object, and validate what it got, rather
than trusting the handle.

**Naming discipline.** The name must encode the deployment identity that the path
encodes today (project, environment, site, machine), must be sanitized of
backslashes beyond the namespace prefix, and is length-bounded. A name collision
fences two unrelated deployments against each other; a name that is too specific
recreates the path-as-configuration problem in a new place.

**Reduced observability with default tooling.** An operator can see a lock file
with `dir`. Seeing a named mutex and its owner needs Process Explorer, `handle.exe`,
or WinObj, which are not installed by default in a conservative enterprise
environment. `troubleshooting.md:104` becomes wrong and its replacement is harder
to follow.

**Platform-specific by definition.** It removes the possibility of running the
ownership path on Linux. Given the separate decision to go Windows-only, this is
consistent rather than a cost, but it should be recognized as a one-way door for
this component.

### Failure scenarios

| Scenario | Behavior |
| --- | --- |
| Both processes start simultaneously | Kernel arbitrates; one acquires |
| Owner process killed | Mutex abandoned; waiter wakes immediately with `WAIT_ABANDONED` |
| **Owning thread exits, process alive** | **Mutex abandoned while resources are still held; split-brain unless the owning thread is pinned for process lifetime** |
| Owner pauses | Mutex held; no failover; same as today |
| `Global\` unavailable due to missing privilege | Creation fails; must fail fast, never silently fall back to `Local\` |
| Squatter holds the name | Platform never becomes active; must be diagnosable |
| Session 0 isolation misconfigured | Two objects, no exclusion; prevented by mandating `Global\` |

The third row is the one that decides whether this approach is viable.

### Recovery behavior

Better than today in one specific way: abandonment is signalled rather than
inferred. `WAIT_ABANDONED` is not an error and the mutex is acquired normally, but
it tells the new owner that the previous one died mid-operation.

For this platform that signal is informational rather than corrective. The
invariant is that the fence is released only after active resources close
(`runtime.go:290`), so a clean release means resources are down. Abandonment means
the process died, and its sockets and handles died with it. Either way the new
owner's composition is the same. The value is observability: a promotion caused by
a crash becomes distinguishable from a planned handover in operational events,
which is exactly the distinction operators currently cannot make.

### Upgrade impact

**This is the sharpest risk in the whole migration and it is not a property of the
mutex, it is a property of changing primitives.**

During a rolling upgrade, an old process using the file lock and a new process
using the mutex do not exclude each other. They contend for different primitives
and both become active. Two actives on one machine binding the same endpoints is
precisely the failure the fence exists to prevent.

There are three ways through it and only one is sound:

1. Cold cutover per machine: stop both processes, upgrade both, start both. Safe,
   and it forfeits the rolling-upgrade requirement for exactly one release.
2. Dual-primitive transition: the new version acquires both the mutex and the file
   lock during a transition release, so it excludes both old and new peers. Then a
   later release drops the file lock. Preserves rolling upgrade, at the cost of one
   release carrying two primitives.
3. Flag-switched primitive: rejected. A configuration flag that selects the
   primitive means two processes can be configured to use different ones, which is
   the failure itself, promoted to a supported option.

Option 2 is the recommendation, with option 1 as the documented fallback for sites
that prefer a maintenance window. The dual-primitive release is temporary,
mechanical, and testable, and it is the only path that satisfies the brief's
rolling-upgrade requirement during the migration that introduces it.

Note the ordering consequence: the file lock cannot be deleted until every machine
runs at least the dual-primitive release. Lock-file retirement is therefore a
separate, later phase gated on fleet state, not a cleanup bundled into the switch.

### Split-brain prevention

Strictly better than the file lock on identity, and strictly worse on ownership
affinity unless the thread-pinning requirement is met.

| Vector | File lock | Mutex |
| --- | --- | --- |
| Simultaneous start | prevented | prevented |
| Crash | prevented | prevented, and signalled |
| Stall or pause | prevented | prevented |
| Network filesystem | **not prevented** | not applicable |
| Config mismatch between processes | **not prevented** | prevented |
| Duplicate install under another path | **not prevented** | prevented |
| Owning thread exits, process alive | not applicable | **not prevented without pinning** |
| Session isolation | not applicable | prevented by mandating `Global\` |
| Mixed-version processes during upgrade | n/a | **not prevented without a dual-primitive release** |

### Operational complexity

Higher than today, and the increase is real rather than incidental. New concerns:
namespace and privilege, DACL design and the service account model, squatting
detection, and a runbook that requires tools an operator may not have. Set against
that, three provisioning constraints disappear: local-filesystem-only, matching
`instance_dir`, and the writable-directory dependency for ownership.

Net: fewer ways to misconfigure, more ways to misunderstand.

## 6. Recommendation

**Adopt the Windows Named Mutex as the ownership primitive, conditional on three
preconditions. If any cannot be met, keep the file lock.**

The conditions are not preferences. Each corresponds to a failure mode above that
has no other mitigation.

1. **Thread affinity is guaranteed.** A dedicated OS thread, pinned for the life
   of the process, performs every acquire, wait, and release, and exits only when
   the process does. Without this the mutex is less safe than what it replaces.
2. **The `Global\` namespace is mandatory and validated at startup.** No fallback
   to `Local\`, no configuration option to choose. A process that cannot create or
   open the global object fails fast and loudly.
3. **The transition preserves mutual exclusion across versions.** One release
   holds both primitives, and the file lock is retired only after the fleet has
   passed through it.

### Why adopt

The decisive argument is not latency. It is that **ownership identity should be a
machine fact, not a configuration string.** The three unprevented split-brain
scenarios in the current design (network filesystem, `instance_dir` mismatch,
duplicate install) all reduce to the fence being scoped by a path that two
processes must agree on and that nothing validates. A machine-scoped kernel object
removes the entire category rather than documenting each instance of it.

The removal of the 100 ms poll is a genuine benefit and worth having, but it is
secondary. So is the abandonment signal, which improves operational insight
without changing behavior. If those were the only benefits, the case for changing
a working primitive would be weak.

### Why the recommendation is conditional

The brief asks to prefer kernel-managed over filesystem-managed ownership
semantics. That preference is sound in general and it is important to be precise
about what the kernel is managing here: `LockFileEx` is *also* kernel-managed
exclusion. The current design is not managing ownership in user space. The
difference is the object's namespace and its affinity, not the presence of the
kernel.

And on affinity the file lock is the safer of the two. A file lock is held by a
process through a handle; a mutex is held by a thread. Trading process affinity
for machine-scoped naming is a good trade only when thread affinity is explicitly
re-established. That is condition 1, and it is why the recommendation is not
unqualified.

### On the mutex as fencing authority

The brief asks the mutex to be the authoritative ownership *and* fencing
mechanism. It can be the first without qualification. The second needs precision,
because overstating it would put weight on the primitive that it cannot carry.

A mutex provides mutual exclusion. It does not provide a fencing token: there is
no monotonic value a demoted owner carries into an operation that a downstream
component can reject.

For this system that gap is already closed by construction, and it is worth
recording why rather than adding a mechanism that duplicates it:

- Ownership is released only after active resources close (`runtime.go:290`), so a
  clean release means there is nothing left to fence.
- On a crash the process's sockets, handles, and embedded server die with it, so
  there is nothing left to fence.
- On a stall the owner keeps the mutex and no second owner exists, so there is
  nothing to fence against.

The residual case is exactly the thread-affinity hazard: a live process that lost
ownership while still holding resources. Condition 1 prevents it. **Fencing in
this design is achieved by the release-ordering invariant plus thread pinning, and
the mutex is the ownership authority that makes those two sufficient.** If the
ordering invariant is ever weakened, an explicit ownership generation counter
becomes necessary and the mutex alone will not substitute for it.

This distinction should be written into the ownership package's documentation, so
a later change cannot quietly assume the primitive is doing more than it is.
