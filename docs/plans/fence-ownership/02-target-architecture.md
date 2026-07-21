# Phase 3: Target architecture

Sections 7 and 8 of the required output.

## 7. Target architecture

The change is deliberately narrow. One primitive is replaced behind an ownership
abstraction that already exists in all but name. Composition, lifecycle, transfer
ordering, and status reporting are unchanged.

```text
platform/cmd
  -> internal/app                composition, unchanged
       -> internal/redundancy    Fence, roles, states, status: interface unchanged
            -> ownership          NEW: named-mutex ownership primitive
                 - dedicated pinned OS thread
                 - Global\ namespace, explicit DACL
                 - kernel wait, no polling
```

`utils/filelock` is retired at the end of the migration. `redundancy.Fence` keeps
its five-method surface (`OpenFence`, `TryAcquire`, `Acquire`, `Release`, `Held`)
so no consumer changes, and gains an acquisition-cause result so abandonment is
reportable.

### What does not change

Stated explicitly, because minimizing disruption is a requirement and because each
of these is load-bearing.

- The transfer ordering invariant (`redundancy/doc.go:21`). Ownership is released
  only after active resources close. This is what makes the shared endpoint set
  safe and it is unaffected by the primitive.
- The shared endpoint model. Both processes compose the same addresses; the owner
  binds them.
- The lifecycle state graph (`redundancy/state.go:37`).
- Process roles `primary` and `standby` as operational identities distinct from
  state.
- Status files as evidence and never as ownership. They keep their format and
  their consumers.
- Non-expiring ownership. No lease, no timeout, no preemption.
- The rule that a machine which opted out of warm standby refuses a second
  process.

### Ownership model

| Property | Value |
| --- | --- |
| Scope | one machine, one deployment identity |
| Authority | successful named-mutex acquisition, and nothing else |
| Namespace | `Global\` only, no fallback, validated at startup |
| Name | derived from project, environment, site, machine; sanitized; length-bounded |
| Affinity | one dedicated OS thread, pinned for process lifetime |
| Expiry | none |
| Preemption | none |
| Granted to | exactly one process at a time |

Ownership derives from the deployment identity compiled into the binary, not from
runtime configuration. This is the point of the change: it matches how the
platform already treats identity (`docs/01-architecture.md:38`) and it removes
`instance_dir` from the ownership decision entirely.

### Primary instance

**Responsibilities.** The preferred owner. Started first by deployment tooling and
expected to hold ownership in steady state. When it holds ownership it composes
the public HTTP listener, the embedded NATS server and JetStream storage, the
durable domain handlers, and lifecycle readiness publication.

**Ownership requirements.** Must hold the mutex to compose any of the above. On
startup it attempts acquisition once, non-blocking. Holding is not implied by the
role: a primary that finds the mutex held runs as a standby and waits, which is
how a returning primary reclaims through handover rather than by stealing
(`docs/01-architecture.md:317`).

### Standby instance

**Responsibilities.** Maintains a warm client-only projection so that promotion is
fast. Writes its own status. Waits for ownership. Owns no public listener, no
durable handler, no readiness publication, no embedded server, and no storage.

**Ownership requirements.** Must not hold the mutex. Must not compose any active
capability. Waits in a kernel wait rather than a poll loop. On acquisition it
closes the client-only composition first, then activates. A standby whose wait is
cancelled ends without acquiring and without promotion.

More than one standby is permitted by this model. The mutex arbitrates any number
of waiters; the current packaging defines one, and nothing in the ownership design
depends on that count.

### The named mutex

**Ownership authority.** Absolute and exclusive. A process is Primary if and only
if it currently holds the mutex. There is no secondary signal, no tiebreak, no
status-file input, and no operator override. Loss of ownership immediately
disqualifies a process from acting as Primary.

**Fencing authority.** Precise scope, per the reasoning in `01-assessment.md`. The
mutex provides mutual exclusion. Fencing is achieved by two invariants that the
mutex makes sufficient:

1. Active resources are closed before ownership is released.
2. The owning thread is pinned for the life of the process, so ownership cannot be
   abandoned while the process still holds resources.

The mutex is not a fencing token and must not be documented as one. If invariant 1
is ever weakened, an explicit monotonic ownership generation must be introduced;
the primitive will not substitute for it.

## Mutex lifecycle

```text
process start
  derive name from compiled deployment identity
  start dedicated ownership thread, pin it
  create-or-open Global\<name> with explicit DACL
    created  -> this process defined the security descriptor
    opened   -> validate the existing object; a squatted object is a startup failure
    denied   -> fail fast; never fall back to Local\
acquire, non-blocking
  acquired            -> Primary
  acquired abandoned  -> Primary; record that the previous owner died
  not acquired        -> Standby
standby wait
  kernel wait on {mutex, cancellation event}
  mutex signalled  -> acquired, possibly abandoned -> activate
  cancel signalled -> end without acquiring, no promotion
release
  only after active resources have closed
  performed on the same pinned thread that acquired
process exit
  kernel abandons the mutex; the next waiter wakes immediately
handle
  closed at process exit; the object disappears when the last handle closes
```

### Startup sequence

Unchanged except for the primitive and one added validation.

1. Parse arguments, load configuration, resolve the role against the descriptor's
   standby policy.
2. Open the operations recorder. Install shutdown signalling.
3. **Start the pinned ownership thread and create-or-open the mutex. Validate the
   namespace and the security descriptor. Fail fast on any of: missing privilege,
   inability to use `Global\`, or an object the platform did not create and cannot
   validate.**
4. Attempt acquisition, non-blocking.
5. Owner: activate (steps unchanged from `docs/01-architecture.md:240`). Validate
   config and probe storage, start embedded NATS, create or validate the journal,
   attach the projector and catch up, attach durable handlers and drain, catch up
   again, publish readiness, serve HTTP.
6. Non-owner with standby enabled: run the standby sequence.
7. Non-owner with standby disabled: fail with the existing error.

Step 3 is the only new step. It runs before any socket or storage is touched,
consistent with the existing principle of validating configuration early.

### Standby sequence

1. Write status `standby`.
2. Open the client-only composition, with retry, exactly as today.
3. Enter the kernel wait on the mutex and the cancellation event. **No polling.**
4. On acquisition: write `activating`, close the client-only composition, then
   activate. Ordering is unchanged.
5. On cancellation: stop without acquiring and without promotion.

### Ownership acquisition flow

```text
                     +---------------------------+
                     | pinned ownership thread   |
                     +---------------------------+
                                 |
                    create-or-open Global\<name>
                                 |
                     validate namespace + DACL
                                 |
                        non-blocking acquire
                        /                  \
                  acquired                not acquired
                     |                         |
          +----------+---------+         standby enabled?
          |                    |          /            \
       normal            abandoned      yes             no
          |                    |         |               |
   record planned      record crash   kernel wait     fail fast
   activation          promotion       on mutex
          \                    /       + cancel
           \                  /            |
            +----> activate <--------------+
```

### Ownership release flow

```text
shutdown signalled, or serving stops
  stop HTTP intake, drain in-flight requests
  stop durable handlers, finish held delivery
  publish platform.event_fabric.stopping
  stop the projector, close the transport
  close the embedded NATS server
  write status stopping
  release the mutex        <-- on the pinned owning thread, only now
  next waiter wakes immediately in the kernel
```

The release point is unchanged from `runtime.go:290`. The only difference is that
the waiter wakes without a poll interval.

### Crash recovery behavior

- The kernel abandons the mutex when the process terminates. The waiter wakes
  immediately and acquires with an abandonment indication.
- The acquirer records that the previous owner died rather than released, which is
  a distinction the current design cannot make.
- No state reconciliation is required. The dead process's sockets, handles, and
  embedded server died with it.
- No stale-object cleanup exists or is needed. There is no file, and the kernel
  object disappears when the last handle closes.
- A process that dies mid-activation abandons the mutex the same way. The next
  waiter performs a full activation, which is idempotent by design because it
  replays the journal at every start.

### Controlled switchover process

Explicit and unchanged in shape, now with a defined trigger.

1. Verify the target standby's status is fresh, its PID is live, `promotable=true`,
   `last_error` is empty, and its projection is caught up
   (`docs/01-architecture.md:340`).
2. Gracefully stop the current owner through the service manager.
3. The owner runs the release flow above. Ownership is released last.
4. The standby's kernel wait returns immediately. It activates.
5. Wait for the promoted process to report `active`.
6. If reclamation by the preferred primary is wanted, start the primary. It finds
   the mutex held and waits as a standby, then repeat from step 1 in the other
   direction.

The primary never steals ownership from a live standby. Reclamation is always this
same procedure run in the opposite direction.

### Upgrade process

This is new capability, not a modification of an existing one. Nothing in the
repository currently implements or documents an upgrade.

Per-machine rolling upgrade, standby-enabled machine:

1. Verify the machine has a healthy owner and a promotable standby.
2. Upgrade the standby's binary. Stop the standby service, replace the package,
   start it. It rejoins as a waiter and warms its projection.
3. Wait for the standby to report `standby`, `promotable=true`, empty
   `last_error`.
4. Perform a controlled switchover. The upgraded process becomes the owner.
5. Upgrade the now-standby former owner the same way.
6. Optionally switch back so the preferred primary owns again.

Service interruption is bounded by one controlled switchover, measured at 177 to
275 ms on the development baseline, and expected to improve once the 100 ms poll
is removed.

For a machine with `standby.disabled = true` there is no rolling upgrade and there
cannot be one: a single process cannot hand over to itself. Those machines take a
stop-upgrade-start outage. This limitation should be stated in the runbook rather
than discovered during a maintenance window.

Site-level ordering is a separate concern. Storage machines hold the journal
replica group, so upgrading them concurrently risks losing quorum
(`docs/01-architecture.md:176`). The upgrade runbook must sequence storage
machines one at a time and wait for the replica group to be healthy between them.

### Fencing model

| Threat | Control |
| --- | --- |
| Two processes composing active resources simultaneously | mutex exclusivity |
| Ownership released while resources are still up | release ordering invariant |
| Ownership abandoned while the process still runs | pinned owning thread |
| Stalled owner waking into a second active | non-expiring ownership; no lease to expire |
| A process acting as Primary without ownership | ownership checked at composition, not cached |
| Mixed-version processes not excluding each other | dual-primitive transition release |
| Unrelated local process holding the name | DACL, plus create-versus-open detection |

### Security considerations

- **Explicit DACL at creation.** Grant only the service identities the platform
  runs under. Do not accept the default security descriptor and do not grant world
  access.
- **Define the service account model.** No account model is documented anywhere
  today. The primary and standby must run under identities that can both open the
  object. This must be written down before the DACL can be designed.
- **Detect open-versus-create.** If the object already existed at first startup,
  the platform did not set its DACL. Treat that as a startup failure with a
  diagnosable message rather than proceeding on a handle whose access control is
  someone else's.
- **`Global\` requires `SeCreateGlobalPrivilege`.** Services and administrators
  hold it by default. Document it as a deployment prerequisite and fail fast with
  a specific error when it is missing, rather than producing a generic access
  denial.
- **Local denial of service remains possible.** A sufficiently privileged local
  process can squat the name. This is not worse than the file lock, where any
  process with write access to `instance_dir` can do the same, but it must be
  detectable rather than silent.
- **Ownership stops depending on filesystem ACLs.** This removes the current
  reliance on ACLs the platform neither sets nor verifies.

### Observability strategy

Operational events through `internal/operations`, which is deliberately
independent of the Event Fabric (`docs/01-architecture.md:59`) and therefore
available when the journal is not.

| Event | Carries |
| --- | --- |
| `platform.ownership_object_created` | name, namespace, DACL summary |
| `platform.ownership_object_opened` | name; a warning, since the platform did not create it |
| `platform.ownership_acquired` | cause: initial, promotion, reclamation; whether abandoned |
| `platform.ownership_abandoned_detected` | previous owner died rather than released |
| `platform.ownership_waiting` | this process is a waiter |
| `platform.ownership_released` | clean release |
| `platform.ownership_denied` | privilege or access failure, with the specific cause |

The abandonment distinction is the main observability gain and should be surfaced
in the status file and the acceptance checks, not only in events. An operator
should be able to answer "was this failover planned?" without correlating logs.

Because a named mutex is not visible with ordinary tools,
`docs/operations/troubleshooting.md:104` must be rewritten. The replacement should
lead with the owner PID reported in status and events, and mention Process
Explorer or `handle.exe` only as a secondary confirmation.

## 8. Ownership state model

Process lifecycle states are unchanged (`redundancy/state.go:37`). Ownership is a
separate axis crossed with them.

| Lifecycle state | Holds mutex | Composes active resources |
| --- | --- | --- |
| `starting` | no | no |
| `standby` | no | no |
| `activating` | **yes** | in progress |
| `active` | **yes** | yes |
| `stopping` | yes until resources close | tearing down |
| `failed` | no | no |

The invariant: **`activating` and `active` are exactly the states in which a
process holds the mutex.** A process in any other state that holds it, or in these
states without it, is a defect and should be assertable in tests.

```text
        start
          |
      starting
       /     \
 acquired   not acquired
      |          |
      |       standby ---- cancelled ----> stopping
      |          |
      |     kernel wait returns (acquired)
      |          |
      +-----> activating ---- failure ----> failed
                 |
              active ---- failure ----> failed
                 |
              stopping
                 |
        release mutex (last)
                 |
               exit
```

Transitions relevant to ownership:

| Transition | Ownership event |
| --- | --- |
| `starting` to `activating` | acquired on first attempt, initial activation |
| `starting` to `standby` | not acquired, becomes a waiter |
| `standby` to `activating` | kernel wait returned; promotion, or reclamation if role is primary |
| `activating` to `active` | none; ownership already held |
| `active` to `stopping` | none; ownership held through teardown |
| `stopping` to exit | released after resources closed |
| any to `failed` | released, then failure recorded |
| process death in any state | abandoned by the kernel |
