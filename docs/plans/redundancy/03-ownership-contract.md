# Stage 03: The lock in the blueprint, ownership in the runtime

**Effort:** Medium. **Complexity:** Medium. **Depends on:** stage 01.

## Intent

`fence` disappears. In its place, two words that mean different things:

- a **lock** is the object instances contend for — a Windows Named Mutex. It is
  configuration. It is authored, carried in the deployment descriptor, and opened.
- **ownership** is what holding that lock means at runtime. It is behavior. It is
  acquired, validated, released, and transferred.

The blueprint and the descriptor talk about the lock. The runtime talks about
ownership. Neither says fence.

`lock` is not banned vocabulary. The goal bans *distributed lock*, which is a
different thing: a distributed lock coordinates across machines by consensus. This
is one kernel object on one host with exactly two contenders.

## Where the lock is authored

Under `standby {}`, because the lock exists only when a Standby Instance is
deployed:

```hcl
platform {
  api        { port = 8080 }
  winservice { name = "opdl-customer-a-north-local-server-primary" }
  nats       { client_port = 4222  cluster_port = 6222 }

  standby {
    disabled = false

    lock { windows_mutex = "Global\\opdl-customer-a-north-local-server" }

    api        { port = 8081 }
    winservice { name = "opdl-customer-a-north-local-server-standby" }
    nats       { client_port = 4322  cluster_port = 6322 }
  }
}
```

A machine that opts out of a standby has nothing to contend with. It states
`disabled = true` and authors no lock, and no lock reaches its descriptor.

This is the same rule every other block in `standby {}` already follows:
authoring something for an instance the machine does not deploy states a decision
that can never take effect.

### Why the descriptor puts it back at machine level

Recommended shape, and decision D1:

```json
"lock": {"windows_mutex": "Global\\opdl-customer-a-north-local-server"},
"instances": {"primary": {...}, "standby": {...}}
```

present only when the standby is deployed, absent otherwise.

The blueprint and the descriptor are answering different questions. The blueprint
records **a decision**, and the decision to have a lock is part of the decision to
deploy a standby, so it is authored there. The descriptor records **a resolved
runtime fact**, and at runtime the lock belongs to neither instance — it is the one
thing they share (goal:70). Nesting it under `instances.standby` would say it is
the standby's, which is the one thing it is not.

The asymmetry is the point, not an accident. Say so in both doc comments.

## Authored, not derived

Today the object name is derived: `<namespace>.fence.<digest>`, where the digest
covers project, environment, site, and machine. A blueprint authors only the
namespace.

That changes. The blueprint authors the whole name.

This is a deliberate trade and both halves need stating.

**What is gained.** The name becomes readable. `opdl.fence.d8558e3e3549dc689184c77b74e544f3`
tells an operator holding a `handle.exe` listing nothing at all;
`Global\opdl-customer-a-north-local-server` tells them exactly which machine's
platform is holding it. Ownership is invisible to ordinary tooling — that is the
whole reason the object name is printed at startup — so making it legible has real
operational value.

**What is lost, and why it is affordable.** The digest existed so two machines
could never be given the same object. Two machines are two hosts, and each host has
its own `Global\` kernel namespace, so the same name on two machines is harmless in
a real deployment.

It stops being harmless when several machines run on **one** host. That is exactly
what the scenario harness does, and it is why the harness renders a per-run random
namespace today. Under authored names the harness must render a distinct
`windows_mutex` per run and per machine instead. Same requirement, different place.

The other loss is quieter: a copy-pasted blueprint machine now carries a
copy-pasted mutex name, and nothing about the deployment looks wrong. See D2.

## The `Global\` trap

A Windows named object lives in a namespace, and which one is not cosmetic.

`Global\` is machine-wide. `Local\` is **session-scoped**. Two instances running as
Windows Services are in session 0 together, so `Local\` might appear to work in
testing and then fail the moment anything runs interactively — two sessions, two
separate mutexes, both instances Active, no error anywhere.

That is a silent split brain caused by one word in a blueprint.

The name is authored, so the builder must reject anything that is not `Global\`.
This is not a preference; it is the one validation in this stage that prevents a
correctness failure rather than a startup failure.

Validation must check: the name begins with `Global\`, contains exactly that one
backslash, is at most 260 characters (`utils/winmutex` bounds it), and is not blank
or padded.

## Ownership becomes conditional

The consequence with the longest reach, and the one to settle before writing code.

Today every instance opens the object and calls `TryAcquire`; acquiring is what
makes it Active. With no lock on a standby-less machine, there is nothing to
acquire, so **a lone Primary Instance is Active by construction.**

That is consistent with the goal — "Mutex Owner → Active Instance" describes a
machine that has a mutex — but it removes a property the platform has today:
starting the same instance twice currently fails cleanly with "another process
already holds Primary Ownership".

Without a lock, the second copy instead fails when it tries to bind a port already
held. That is still a failure, but a messier one: it may bind its API and fail on
NATS, or the reverse, and the message names a socket rather than the actual problem.

Options are decision D3. This must be answered before stage 04, which is where a
second copy of an instance stops being hypothetical.

## Target

| Now | Target |
| --- | --- |
| blueprint `platform.fence { namespace }` | `platform.standby.lock { windows_mutex }` |
| `DefaultFenceNamespace`, `maxFenceNamespace` | gone; the name is authored in full |
| `Machine.FenceNamespace()` | `Machine.Lock()`, nil when no standby |
| derived `<ns>.fence.<digest>` | nothing derived |
| descriptor `Fence{Object}` / `fence.object` | `Lock{WindowsMutex}` / `lock.windows_mutex`, omitted when no standby |
| `redundancy.Ownership` (the handle type) | `redundancy.Lock`; what it grants stays Ownership |
| `platform.fence_opened` | `platform.lock_opened` |
| `platform.fence_open_failed` | `platform.lock_open_failed` |
| `platform.fence_waiting` | `platform.ownership_waiting` |
| `platform.fence_acquired` | `platform.ownership_acquired` |

The event split follows the word split: opening the object is a lock operation,
and who is Active is an ownership fact. See D4 if the mixed prefix is unwelcome.

## The upgrade window

The lock name changes, so the object two instances contend for changes.

A machine running one instance on the old derived name and one on the new authored
name has **two locks and no contention.** Both acquire, both go Active, and nothing
reports an error. That is a silent split brain and it is the same failure the
`Local\` trap produces.

Both instances of a machine must be stopped and restarted together for this
release. The rolling upgrade in `docs/operations/upgrade.md` deliberately runs the
two instances on different binaries for a window, and during that window this
change is unsafe. The runbook must say so for this release specifically.

## Decisions

**D1. Descriptor placement.** Machine-level `lock`, omitted when the standby is
disabled, as recommended above? Or mirror the blueprint at
`instances.standby.lock`? Recommendation: machine-level, with both doc comments
explaining why the two files disagree on purpose.

**D2. Duplicate mutex names within a project.** Harmless across real machines,
almost certainly a copy-paste mistake, and catastrophic if those machines ever
share a host. Recommendation: reject duplicates within a project. The check is
cheap, and the failure it prevents is silent.

**D3. What replaces the lock for a machine with no standby?** Options: accept that
a lone Primary is Active by construction and let a double start fail on port
binding; or have every instance open a lock regardless, authored at machine level
when there is no standby, which contradicts the placement above; or open an
unshared per-instance object purely as a single-instance guard, which is a
different mechanism wearing the same name. Recommendation: the first, with the
double-start failure made explicit in the deployment runbook. Confirm before
stage 04.

**D4. Event prefixes.** `lock_*` for the object and `ownership_*` for the state, or
one prefix for all four? Recommendation: the split, because the two really are
different facts and an operator asking "who is Active" wants only the second pair.
The cost is that a single grep no longer finds all four.

**D5. Does anything outside the repo match the current event names?** They are the
operational contract this stage breaks. If yes, decide whether to emit both names
for one release.

## Work

1. Blueprint: delete the `Fence` block, constants, and accessor. Add `Lock` under
   `Standby` with `windows_mutex`, required when the standby is deployed and
   rejected when it is not. Validate per the `Global\` rules above and per D2.
2. Resolver: stop deriving a name. Copy the authored value through, and emit no
   lock at all when the standby is disabled.
3. Both descriptors: replace `Fence` with `Lock`, in lockstep. The conformance
   check in `conformance-tests/deployment-descriptors` fails if only one side
   moves, which is the safety net for this step.
4. Runtime: rename the handle type to `Lock`, keep ownership vocabulary for what
   acquiring it means, and handle the no-lock case per D3.
5. Events: rename per the table and D4.
6. Update every blueprint and fixture: `examples/customer-a/project.hcl`,
   `builder/internal/blueprint/testdata/project.hcl`,
   `platform/embedded/deployment.json`, and
   `scenarios/testdata/project.hcl.tmpl`, where the per-run namespace becomes a
   per-run mutex name.
7. Update `docs/01-architecture.md` and the four runbooks, including the
   full-restart note above.

## The prose hazard

`fence` is a common English noun and this codebase used it as one in comments. Sed
the identifiers, replace prose with explicit before-and-after pairs, then grep the
new words and read every hit.

The last time this exact rename ran, a word-boundary sed produced "the exclusive
machine ownership", "not a ownership token", and "machine ownership ownership
object opened". All of it compiled and none of it means anything. There is no
shortcut; budget for the reading.

Reading is doubled here because there are two target words, and the wrong one in
the wrong place is invisible: "the lock is transferred" and "ownership is opened"
are both wrong and both read fine.

## Validation

- `task all` passes.
- `grep -rin fence` returns nothing outside this plan's own history.
- A blueprint authoring `Local\...`, a bare name with no namespace, or a duplicate
  within the project is rejected at build time with a message naming the machine.
- A machine with `disabled = true` produces a descriptor with no lock, and its
  Primary Instance still starts and serves.
- A machine with a standby produces one lock shared by both instances, and the
  warm standby failover still works, which is what proves they contend for the
  same object.
