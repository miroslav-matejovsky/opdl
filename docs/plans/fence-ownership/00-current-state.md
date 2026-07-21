# Phase 1: Current state analysis

Sections 1, 2, and 3 of the required output. Analysis only, no proposal.

## Scope

The platform's local machine fence: which of the two platform processes on one
machine owns active capabilities. Nothing else.

Out of scope: the site journal, registration, Event Fabric transport, inter-node
coordination, and client-service leadership (planned separately in
`docs/plans/leadership/`).

## 1. Current architecture summary

One machine runs one or two platform processes from the same package, launched
with `-instance primary` or `-instance standby` (`docs/operations/deployment.md:118`).
Both carry the same compiled machine identity and the same configuration. They
are one registration voter, not two.

Ownership is decided by an exclusive OS file lock. The holder owns every active
capability; the other process runs a client-only projector and owns nothing
externally visible.

| Layer | Component | Responsibility |
| --- | --- | --- |
| Primitive | `utils/filelock` | OS file lock, crash-released, context-aware |
| Domain | `platform/internal/redundancy` | fence, process role, lifecycle state, status |
| Composition | `platform/internal/app` | contends, branches, composes active or standby |
| Evidence | status files | operational snapshot, explicitly not ownership |

### The lock primitive

`utils/filelock/lock.go` takes the lock through `LockFileEx` with
`LOCKFILE_EXCLUSIVE_LOCK | LOCKFILE_FAIL_IMMEDIATELY` over one byte (`:123`).

`Open` creates the parent directory and does not lock (`lock.go:24`). `TryAcquire`
opens the file `O_CREATE|O_RDWR` and takes the lock (`lock.go:40`). `Release`
unlocks and closes (`lock.go:96`). `Held` reports local handle state (`lock.go:109`).

**`Acquire` polls.** `lock.go:12` sets `pollInterval = 100 * time.Millisecond`, and
`lock.go:58` loops `TryAcquire`, timer, context check. There is no kernel wait and
no notification on release.

### The fence

`platform/internal/redundancy/fence.go` scopes the lock to a deployment identity.
The path is `<instance_dir>/<project>-<environment>-<site>-<machine>/active.lock`
(`fence.go:17`, `fence.go:27`). `FenceFileName` is `active.lock` (`fence.go:14`).

Both processes of a machine compute the same path, so `role` is carried for
diagnostics only (`fence.go:57`). The documented properties (`fence.go:41`):
exclusive, non-expiring, no lease, no timeout, released on explicit release or
process exit.

## 2. Ownership determination logic

`platform/internal/app/runtime.go:61`, `runProcess`:

```text
OpenFence(path, role)
TryAcquire()
  acquired -> runFencedActive(kind = initial activation)
  not acquired
    standby slot disabled -> hard error, exit
    standby slot enabled  -> runStandby
```

Ownership is a single non-blocking attempt at startup. There is no negotiation, no
preference, and no arbitration beyond the OS lock. A machine that opted out of
warm standby refuses to start a second process at all (`runtime.go:81`).

## 3. Primary/Standby lifecycle

`redundancy/state.go:37` defines the legal graph:

| From | To |
| --- | --- |
| `starting` | `standby`, `activating`, `stopping`, `failed` |
| `standby` | `activating`, `stopping`, `failed` |
| `activating` | `active`, `stopping`, `failed` |
| `active` | `stopping`, `failed` |
| `stopping` | `failed` |
| `failed` | terminal |

There is no path back into `active` from `stopping` or `failed`
(`state.go:33`). A process that gave up its resources does not reclaim them; a new
process is started instead.

`ProcessRole` (`primary`, `standby`) is an operational identity independent of
state. Both roles traverse the same graph. A promoted standby is `active` while
still being role `standby`.

## 4. Failover behavior

`runStandby` (`runtime.go:164`) runs two things concurrently:

1. A goroutine blocked in `fence.Acquire(waitCtx)` (`runtime.go:172`). On success
   it cancels the standby composition.
2. The client-only projection, opened with retry (`openWaitingStandby`,
   `runtime.go:214`, `standbyRetryInterval = 200ms`, `runtime.go:26`).

On acquisition (`runtime.go:190` onward): write `activating`, close the
client-only composition, then `runFencedActive` with kind `standby promotion`, or
`primary reclamation` when the acquiring role is primary (`runtime.go:203`).

The transfer invariant is ordering, documented at `redundancy/doc.go:21`:

```text
active closes HTTP and Event Fabric
active embedded NATS stops
active releases fence
waiter acquires fence
waiter opens embedded NATS on the same endpoints
waiter catches up and serves HTTP
```

Both processes share one endpoint set, so promotion does not change the address
the rest of the site was told (`doc.go:13`). A bind failure after acquisition
records a failed status and never falls back to another port (`doc.go:42`).

Measured on the development baseline (`docs/01-architecture.md:367`): forced-kill
promotion 126.8 to 182.6 ms, planned handover 177.8 to 275.8 ms, three samples on
one Windows host.

## 5. Startup sequence

`app.Run` (`app.go:22`): parse flags, load config, resolve role against the
descriptor's standby policy, open the operations recorder, install
`signal.NotifyContext(os.Interrupt, syscall.SIGTERM)` (`app.go:54`), call
`runProcess`.

Active startup order is strict (`docs/01-architecture.md:240`): validate config
and probe storage, start embedded NATS and connect, create or validate the
journal, attach the ordered projector and catch up to a captured high-water,
attach durable handlers and drain retained work, catch up again, publish
`platform.event_fabric.ready`, then serve HTTP. Bounded by `catch_up_timeout`.

Deployment order (`deployment.md:127`): storage machines together, then other
machines, then each preferred primary waiting for status `active`, then its
standby waiting for status `standby` with `promotable=true` and empty
`last_error`.

## 6. Shutdown sequence

`serveListener` (`app.go:92`) reverses startup. HTTP intake stops and in-flight
requests drain, then handlers stop, then the node states
`platform.event_fabric.stopping` while the journal can still accept it, then the
projector stops and the transport closes (`docs/01-architecture.md:299`).

`runFencedActive` releases the fence only after `runActive` returns
(`runtime.go:290`), which is what makes the transfer ordering hold.

Full machine shutdown stops the primary service and then the standby service. The
standby may briefly promote between those stops, so the second stop is mandatory
(`deployment.md:134`).

## 7. Upgrade sequence

**There is none.** A repository-wide search for upgrade, rolling, or switchover
returns no procedure, no documentation, no code path, and no test.

The pieces exist. `docs/01-architecture.md:340` describes handover: verify the
standby is caught up, gracefully stop the active process, wait for the other to
become `active`. `docs/01-architecture.md:317` states a returning primary reclaims
only through graceful handover and never steals from a live standby.

But no document composes those into an upgrade of the binary, no tooling
implements it, and no scenario proves it. The brief asks for rolling upgrades with
minimal interruption; that capability does not currently exist in any form. This is
the largest gap found in Phase 1 and it is independent of the ownership primitive.

## 8. Health monitoring mechanisms

`startStatus` (`runtime.go:307`) writes an initial status synchronously, then
every `statusInterval = 1s` (`runtime.go:24`). Each write samples
`fabric.State(ctx)` and records role, state, PID, applied, high water, lag,
promotable, updated-at, last error.

Two feedback paths make monitoring load-bearing rather than passive:

- **Lag bound.** When lag exceeds `cfg.LagBound()`, `promotable` goes false and
  `onLagExceeded` cancels serving (`runtime.go:109`). A node too far behind stops
  serving rather than answering from a stale view.
- **Status write failure.** `onStatusFailure` also cancels serving
  (`runtime.go:115`), because deployment tooling must not act on a stale file
  during handover (`runtime.go:305`).

The second is worth stating plainly: **a failure to write a status file stops the
active process.** The status file is documented as evidence and not ownership
(`status.go:19`), but its writability is a liveness dependency of the active role.

Acceptance checks are operational events plus status-file assertions
(`deployment.md:138`).

## 9. Coordination mechanisms between process instances

Between the two processes on one machine, exhaustively:

| Mechanism | Direction | Purpose |
| --- | --- | --- |
| `active.lock` | mutual exclusion | the only ownership decision |
| Status files | process to tooling | evidence, read by deployment and tests |
| Shared endpoint set | implicit | both compose the same addresses; exclusivity comes from the fence |
| Shared `instance_dir` | implicit | both must be configured identically |

The two processes never talk to each other. There is no handshake, heartbeat,
negotiation, or message of any kind between them. All coordination is mediated by
the OS lock and by the ordering invariant each process observes independently.

## 10. Existing IPC mechanisms

| Mechanism | Scope | Notes |
| --- | --- | --- |
| HTTP API | machine to network | active process only, bound to descriptor address |
| NATS | machine to machine | out of scope |
| File lock | process to process, local | the fence |
| Status files | process to tooling, local | filesystem as IPC |
| stderr and JSONL | process to operator | `internal/operations`, independent of NATS by design |

**There is no named pipe, named mutex, named event, shared memory, socket, or any
other kernel IPC object in the repository.** The proposed mutex would be the first
named kernel object the platform creates.

## 11. Potential split-brain scenarios

Assessed against the current implementation.

| # | Scenario | Prevented today | By what |
| --- | --- | --- | --- |
| 1 | Both processes start simultaneously | yes | `LockFileEx` exclusivity is atomic |
| 2 | Active crashes, standby promotes | yes | OS drops the lock at handle close; ports die with the process |
| 3 | Active pauses (VM suspend, debugger, long stall) | yes | non-expiring lock; a paused holder keeps it and can never wake into a second active (`fence.go:44`) |
| 4 | Active releases before resources close | yes, by construction | release happens after `runActive` returns (`runtime.go:290`) |
| 5 | Returning primary steals from live standby | yes | it contends for the same fence and loses (`runtime.go:72`) |
| 6 | `instance_dir` on a network filesystem | **no** | documented prohibition only (`deployment.md:60`) |
| 7 | Two processes configured with different `instance_dir` | **no** | nothing validates that they match |
| 8 | Same machine package installed twice under different paths | **no** | different paths give different fences |
| 9 | Status file consulted as ownership | mitigated | documented repeatedly, not enforced |

Scenarios 6, 7, and 8 share a root cause: **ownership identity is a filesystem
path, and a path is configuration.** Two processes agree on ownership only if they
agree on a string. Nothing checks that they do. This is the strongest structural
argument in the current design for a machine-scoped kernel object, and it is a
separate argument from latency.

Scenario 3 deserves emphasis because it is a property to preserve, not a defect.
The non-expiring lock means a stalled holder blocks failover rather than risking
two actives. That is the correct trade for this system and any replacement must
keep it.

## 12. Recovery logic after crashes

The OS releases the lock when the process's handle closes, including on abnormal
termination. Recovery is therefore implicit: the waiting standby's next
`TryAcquire` succeeds, within one poll interval.

The lock *file* is never deleted. It persists across crashes and restarts and
carries no content. There is no stale-lock detection, no PID check, no timeout,
and no cleanup, because none is needed: the lock is a kernel state on an open
handle, not the file's existence.

Status files are not cleaned up either. A stale status after a crash is
historical diagnostics (`status.go:19`), distinguishable by its PID and
`updated_at`.

There is no crash-recovery path for partially released resources. If a process
dies mid-shutdown, its sockets, handles, and embedded server die with it.

## 13. Operational constraints

| Constraint | Source |
| --- | --- |
| `instance_dir` must be on a local filesystem, not NFS or SMB | `deployment.md:60`, `fence.go:24` |
| Primary and standby must share `instance_dir` and configuration | `deployment.md:59` |
| Both processes need write access to the fence directory | `filelock.Open`, `0o755` dir, `0o600` file |
| Status must be writable or the active process stops | `runtime.go:115` |
| Full shutdown must stop primary then standby | `deployment.md:134` |
| Handover requires a fresh live standby status with `promotable=true` | `docs/01-architecture.md:340` |
| Do not copy one node's store into two live nodes | `deployment.md:64` |

## 14. Security constraints

| Aspect | Current state |
| --- | --- |
| Fence file permissions | `0o600` on the file, `0o755` on the directory (`lock.go:25`, `lock.go:40`). On Windows these mode bits are largely advisory; effective access comes from inherited NTFS ACLs. |
| Fence directory ACL | Not set by the platform. Inherited from `instance_dir`, which is operator-provisioned. |
| Service identity | Not specified anywhere. No documented account model for the two processes. |
| Denial of service | A local process with write access to `instance_dir` can hold the lock and block the platform indefinitely. |
| HTTP API | Anonymous. Authentication and authorization are listed as out of scope (`docs/02-registration.md:162`) and open in `.todo`. |
| Credentials | NATS username and password in a separate file, restricted to the service identity (`deployment.md:70`). |

The security posture of the fence today rests entirely on filesystem ACLs the
platform does not set and does not verify.

## 15. Backward-compatibility requirements

`AGENTS.md:17` states the project is in an experimentation phase and favors
progress and clean code over backwards compatibility.

Concretely, for this work:

- **No wire or contract compatibility is at stake.** The fence is entirely
  internal. It appears in no HTTP contract, no OpenAPI artifact, no SDK, and no
  event payload.
- **No on-disk format compatibility is at stake.** The lock file has no content.
- **Mixed-version processes on one machine must be considered.** During a rolling
  upgrade, an old process and a new process may contend simultaneously. If they
  use different primitives they do not exclude each other, and that is a real
  split-brain window. This is the one genuine compatibility requirement and it is
  addressed in the migration plan.
- **Operational documentation is a compatibility surface.**
  `troubleshooting.md:104` instructs operators to inspect `active.lock`. That
  instruction becomes wrong.

## Dependency map

```text
platform/cmd
  -> internal/app          (Run, runProcess, runActive, runStandby, startStatus)
       -> internal/redundancy   (Fence, ProcessRole, State, Status, Lag)
            -> utils/filelock       (Lock: TryAcquire, Acquire, Release, Held)
                 -> LockFileEx
            -> utils/atomicfile     (Status.Write)
       -> internal/config       (InstanceDir, Address, LagBound, timeouts)
       -> internal/operations   (fence_open_failed, fence_acquired, fence_waiting,
                                 activation_started/completed/failed)
       -> internal/eventfabric  (State for status; not ownership)

consumers of status files (not of the fence):
  docs/operations/deployment.md    startup gating, handover gating
  docs/operations/troubleshooting.md  inspects active.lock directly
  platform/internal/app/app_test.go   waitForProcessState, waitForPromotableStandby
  scenarios/warm_standby_test.go      failover baseline
```

The blast radius of replacing the primitive is small and well-bounded:
`utils/filelock` and `redundancy/fence.go` are the only code that touches the
lock. Everything else consumes the `Fence` type's five methods.

## Current lock-file lifecycle

```text
process start
  Open(path)            create parent dir; no lock; file may not exist yet
  TryAcquire()          O_CREATE|O_RDWR, then LockFileEx/flock, non-blocking
    success -> hold; run active; ...
    failure -> close handle; run standby
                 Acquire(ctx)  poll TryAcquire every 100ms until held or canceled
active resources close
  Release()             unlock, close handle, clear
process exit (clean or crash)
  OS closes the handle and drops the lock
file
  never deleted, never read, never written; content is irrelevant
```

## Failure mode analysis

| Mode | Detection | Current behavior | Residual risk |
| --- | --- | --- | --- |
| Active process killed | standby's next poll, up to 100 ms | standby promotes | up to 100 ms of avoidable latency |
| Active process hangs, handle open | none | fence held; no failover | correct by design; needs external liveness action |
| Active exits without releasing | OS handle close | lock dropped, standby promotes | no signal distinguishes crash from clean release |
| Bind fails after acquisition | immediate | failed status recorded, no port fallback | machine has no active process until restarted |
| Projection lag exceeds bound | status loop, 1 s | stops serving, `promotable=false` | fence still held while not serving |
| Status write fails | status loop, 1 s | stops serving | filesystem fault escalates to an outage |
| `instance_dir` on network FS | none | undefined locking semantics | split-brain, documented prohibition only |
| `instance_dir` mismatch between processes | none | two independent fences | split-brain, nothing validates |
| Third-party process holds the lock | none | platform never starts active | local DoS through filesystem ACLs |
| Fence directory not writable | at `Open` | `fence_open_failed`, process exits | correct fail-fast |

## Risks and assumptions

Assumptions the current design depends on, none of which are verified at runtime:

1. `instance_dir` is on a local filesystem with correct lock semantics.
2. Both processes are configured with the same `instance_dir`.
3. Only platform processes contend for the lock path.
4. The filesystem stays writable for the life of the active process.
5. Filesystem ACLs on `instance_dir` are correctly provisioned by the operator.
6. Exactly one package per machine identity is installed on a machine.

Risks carried into Phase 2:

- **Latency floor.** The 100 ms poll is a fixed cost on every promotion and
  handover, against measured promotions of 126 to 182 ms.
- **Path-as-identity.** Ownership scope is a configuration string, not a machine
  fact. Assumptions 1, 2, and 6 all derive from this.
- **Filesystem as a liveness dependency.** A status write failure stops an
  otherwise healthy active process.
- **No upgrade capability at all.** The brief's rolling-upgrade requirement has
  nothing to build on.
- **No Windows Service integration.** `app.go:54` handles `os.Interrupt` and
  `syscall.SIGTERM`. There is no Service Control Manager integration anywhere in
  the repository, so the "Windows Service deployment model" the brief requires is
  assumed by the documentation and not implemented in the code.
